package paramfetch

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/cheggaaa/pb/v3"
	fslock "github.com/ipfs/go-fs-lock"
	logging "github.com/ipfs/go-log/v2"
	"go.uber.org/multierr"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/xerrors"
)

var log = logging.Logger("paramfetch")

// Retired gateways:
//
//	const gateway = "http://198.211.99.118/ipfs/"
//	const gateway = "https://proofs.filecoin.io/ipfs/" // cluster being shut down

// defaultGateways are tried in order. Each entry must serve files keyed by CID,
// so that <gateway><cid> resolves to the file.
var defaultGateways = []string{
	// Forest (ChainSafe)
	"https://filecoin-proofs.chainsafe.dev/ipfs/",
	// FOC SP: https://vault.ezpdpz.net/params.html
	"https://vault.ezpdpz.net/ipfs/",
}

const paramdir = "/var/tmp/filecoin-proof-parameters"
const dirEnv = "FIL_PROOFS_PARAMETER_CACHE"
const lockFile = "fetch.lock"
const lockRetry = time.Second * 10

var checked = map[string]struct{}{}
var checkedLk sync.Mutex

type paramFile struct {
	Cid        string `json:"cid"`
	Digest     string `json:"digest"`
	SectorSize uint64 `json:"sector_size"`
}

type fetch struct {
	wg      sync.WaitGroup
	fetchLk sync.Mutex

	errs []error
}

func getParamDir() string {
	if os.Getenv(dirEnv) == "" {
		return paramdir
	}
	return os.Getenv(dirEnv)
}

func GetParams(ctx context.Context, paramBytes []byte, srsBytes []byte, storageSize uint64) error {
	if err := os.Mkdir(getParamDir(), 0755); err != nil && !os.IsExist(err) {
		return err
	}

	var params map[string]paramFile

	if err := json.Unmarshal(paramBytes, &params); err != nil {
		return err
	}

	ft := &fetch{}

	for name, info := range params {
		if storageSize != info.SectorSize && strings.HasSuffix(name, ".params") {
			continue
		}

		ft.maybeFetchAsync(ctx, name, info)
	}

	var srs map[string]paramFile

	if err := json.Unmarshal(srsBytes, &srs); err != nil {
		return err
	}

	for name, info := range srs {
		ft.maybeFetchAsync(ctx, name, info)
	}

	return ft.wait(ctx)
}

func (ft *fetch) maybeFetchAsync(ctx context.Context, name string, info paramFile) {
	ft.wg.Add(1)

	go func() {
		defer ft.wg.Done()

		path := filepath.Join(getParamDir(), name)

		err := ft.checkFile(path, info)
		if !os.IsNotExist(err) && err != nil {
			log.Warn(err)
		}
		if err == nil {
			return
		}

		ft.fetchLk.Lock()
		defer ft.fetchLk.Unlock()

		// Re-check after acquiring the in-process mutex — another goroutine
		// may have already fetched the file while we waited.
		if err := ft.checkFile(path, info); err == nil {
			return
		}

		var lockfail bool
		var unlocker io.Closer
		for {
			unlocker, err = fslock.Lock(getParamDir(), lockFile)
			if err == nil {
				break
			}

			lockfail = true

			le := fslock.LockedError("")
			if errors.As(err, &le) {
				log.Warnf("acquiring filesystem fetch lock: %s; will retry in %s", err, lockRetry)
				time.Sleep(lockRetry)
				continue
			}
			ft.errs = append(ft.errs, xerrors.Errorf("acquiring filesystem fetch lock: %w", err))
			return
		}
		defer func() {
			err := unlocker.Close()
			if err != nil {
				log.Errorw("unlock fs lock", "error", err)
			}
		}()
		if lockfail {
			// we've managed to get the lock, but we need to re-check file contents - maybe it's fetched now
			ft.maybeFetchAsync(ctx, name, info)
			return
		}

		if err := ft.doFetch(ctx, path, info); err != nil {
			ft.errs = append(ft.errs, xerrors.Errorf("fetching file %s failed: %w", path, err))
			return
		}
	}()
}

func hasTrustableExtension(path string) bool {
	// known extensions include "vk", "srs", and "params"
	// expected to only treat "params" ext as trustable
	// via allowlist
	return strings.HasSuffix(path, "params")
}

func (ft *fetch) checkFile(path string, info paramFile) error {
	isSnapParam := strings.HasPrefix(filepath.Base(path), "v28-empty-sector-update")

	if !isSnapParam && os.Getenv("TRUST_PARAMS") == "1" && hasTrustableExtension(path) {
		log.Debugf("Skipping param check: %s", path)
		log.Warn("Assuming parameter files are ok. DO NOT USE IN PRODUCTION")
		return nil
	}

	checkedLk.Lock()
	_, ok := checked[path]
	checkedLk.Unlock()
	if ok {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h, _ := blake2b.New512(nil) // errors only happen on invalid, non-nil key
	if _, err := io.Copy(h, f); err != nil {
		return err
	}

	sum := h.Sum(nil)
	strSum := hex.EncodeToString(sum[:16])
	if strSum == info.Digest {
		log.Infof("Parameter file %s is ok", path)

		checkedLk.Lock()
		checked[path] = struct{}{}
		checkedLk.Unlock()

		return nil
	}

	return xerrors.Errorf("checksum mismatch in param file %s, %s != %s", path, strSum, info.Digest)
}

func (ft *fetch) wait(ctx context.Context) error {
	waitChan := make(chan struct{}, 1)

	go func() {
		defer close(waitChan)
		ft.wg.Wait()
	}()

	select {
	case <-ctx.Done():
		log.Infof("context closed... shutting down")
	case <-waitChan:
		log.Infof("parameter and key-fetching complete")
	}

	return multierr.Combine(ft.errs...)
}

// gateways returns the sources to try, in order. An explicit IPFS_GATEWAY
// replaces the defaults rather than preceding them: a node pointed at a private
// mirror must not fall back to public hosts.
func gateways() []string {
	if gw := os.Getenv("IPFS_GATEWAY"); gw != "" {
		return []string{gw}
	}
	return defaultGateways
}

func (ft *fetch) doFetch(ctx context.Context, out string, info paramFile) error {
	gws := gateways()
	if len(gws) == 0 {
		return xerrors.New("no gateways configured")
	}

	var errs []error
	for i := 0; i < len(gws) && ctx.Err() == nil; i++ {
		gw := gws[i]
		err := ft.fetchFromGateway(ctx, out, gw, info)
		if err == nil {
			return nil
		}

		log.Warnf("fetching %s from %s failed (source %d of %d): %s", out, gw, i+1, len(gws), err)
		errs = append(errs, err)
	}

	return multierr.Combine(append(errs, ctx.Err())...)
}

// errUnresumable means a gateway rejected a nonzero resume offset.
var errUnresumable = errors.New("resume offset rejected")

// fetchFromGateway verifies the download and retries from scratch at most once
// if the gateway rejects the resume offset or the resumed content fails validation.
func (ft *fetch) fetchFromGateway(ctx context.Context, out, gw string, info paramFile) error {
	for attempt := 0; ; attempt++ {
		resumed, err := fetchOnce(ctx, out, gw, info)
		retry := resumed
		if errors.Is(err, errUnresumable) {
			retry = true
		} else if err != nil {
			return err
		} else if err = ft.checkFile(out, info); err == nil {
			return nil
		}

		// Neither a rejected offset nor invalid content should be reused.
		if rmErr := os.Remove(out); rmErr != nil && !os.IsNotExist(rmErr) {
			return multierr.Combine(err, rmErr)
		}
		if !retry || attempt > 0 {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return multierr.Combine(err, ctxErr)
		}
		log.Warnf("resuming %s from %s failed: %s; retrying the whole file", out, gw, err)
	}
}

// fetchOnce reports whether a successful download retained any existing bytes.
func fetchOnce(ctx context.Context, out, gw string, info paramFile) (bool, error) {
	log.Infof("Fetching %s from %s", out, gw)

	outf, err := os.OpenFile(out, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return false, err
	}
	defer outf.Close()

	fStat, err := outf.Stat()
	if err != nil {
		return false, err
	}
	haveBytes := fStat.Size()

	header := http.Header{}
	header.Set("Range", "bytes="+strconv.FormatInt(haveBytes, 10)+"-")
	url, err := url.Parse(gw + info.Cid)
	if err != nil {
		return false, err
	}
	log.Infof("GET %s", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url.String(), nil)
	if err != nil {
		return false, err
	}
	req.Close = true
	req.Header = header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && haveBytes > 0 {
		return false, errUnresumable
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return false, xerrors.Errorf("fetching file from %s: %s", url, resp.Status)
	}

	resumed := haveBytes > 0
	if haveBytes > 0 && resp.StatusCode == http.StatusOK {
		// The gateway ignored the Range header and is sending the whole file.
		// Appending would corrupt what we already have, so start over.
		log.Warnf("%s ignored range request for %s, restarting download", gw, out)
		if err := outf.Truncate(0); err != nil {
			return false, err
		}
		haveBytes = 0
		resumed = false
	}

	bar := pb.New64(haveBytes + resp.ContentLength).
		SetCurrent(haveBytes).Start()

	_, err = io.Copy(outf, bar.NewProxyReader(resp.Body))

	bar.Finish()

	if err != nil {
		// A read that dies mid-body yields a bare network error, naming neither
		// the gateway nor the file.
		return false, xerrors.Errorf("reading %s: %w", url, err)
	}

	return resumed, nil
}
