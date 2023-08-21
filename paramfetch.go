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

	fslock "github.com/ipfs/go-fs-lock"
	logging "github.com/ipfs/go-log/v2"
	"github.com/minio/blake2b-simd"
	"go.uber.org/multierr"
	"golang.org/x/xerrors"
	pb "gopkg.in/cheggaaa/pb.v1"
)

var log = logging.Logger("paramfetch")

//const gateway = "http://198.211.99.118/ipfs/"
const gateway = "https://proofs.filecoin.io/ipfs/"
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
	wg sync.WaitGroup

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

var startedFetching = map[string]bool{}
var fetchLk sync.Mutex

func (ft *fetch) maybeFetchAsync(ctx context.Context, name string, info paramFile) {
	ft.wg.Add(1)

	fetchLk.Lock() // Protects startedFetching
	defer fetchLk.Unlock()
	if startedFetching[name] {
		ft.wg.Done()
		return
	}
	startedFetching[name] = true

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

		var lockfail bool
	tryLock:
		unlocker, err := fslock.Lock(getParamDir(), name+"."+lockFile)
		if err != nil {
			lockfail = true

			le := fslock.LockedError("")
			if errors.As(err, &le) {
				log.Warnf("acquiring filesystem fetch lock: %s; will retry in %s", err, lockRetry)
				time.Sleep(lockRetry)
				goto tryLock
			}
			log.Info("lock error: ", err)
			ft.errs = append(ft.errs, xerrors.Errorf("acquiring filesystem fetch lock: %w", err))
			goto tryLock
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

		for i := 0; i < 2; i++ {
			if err := doFetch(ctx, path, info); err != nil {
				ft.errs = append(ft.errs, xerrors.Errorf("fetching file %s failed: %w", path, err))
				return
			}
			err = ft.checkFile(path, info)
			if err != nil {
				if i == 0 {
					log.Errorf("sanity checking fetched file failed, removing and retrying: %w", err)
				}
				// remove and retry once more
				err := os.Remove(path)
				if err != nil {
					ft.errs = append(ft.errs, xerrors.Errorf("remove file %s failed: %w", path, err))
					return
				}
				continue
			}
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

	h := blake2b.New512()
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

func doFetch(ctx context.Context, out string, info paramFile) error {
	gw := os.Getenv("IPFS_GATEWAY")
	if gw == "" {
		gw = gateway
	}
	log.Infof("Fetching %s from %s", out, gw)

	outf, err := os.OpenFile(out, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return err
	}
	defer outf.Close()

	fStat, err := outf.Stat()
	if err != nil {
		return err
	}
	header := http.Header{}
	header.Set("Range", "bytes="+strconv.FormatInt(fStat.Size(), 10)+"-")
	url, err := url.Parse(gw + info.Cid)
	if err != nil {
		return err
	}
	log.Infof("GET %s", url)

	req, err := http.NewRequestWithContext(ctx, "GET", url.String(), nil)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header = header

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	bar := pb.New64(fStat.Size() + resp.ContentLength)
	bar.Set64(fStat.Size())
	bar.Units = pb.U_BYTES
	bar.ShowSpeed = true
	bar.Start()

	_, err = io.Copy(outf, bar.NewProxyReader(resp.Body))

	bar.Finish()

	return err
}
