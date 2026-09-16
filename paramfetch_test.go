package paramfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	logging "github.com/ipfs/go-log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func init() {
	logging.SetAllLoggers(logging.LevelDebug)
}

// small files only
const params = `{
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-0-0-0170db1f394b35d995252228ee359194b13199d259380541dc529fb0099096b0.vk": {
    "cid": "QmcS5JZs8X3TdtkEBpHAdUYjdNDqcL7fWQFtQz69mpnu2X",
    "digest": "0e0958009936b9d5e515ec97b8cb792d",
    "sector_size": 2048
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-0-0-0cfb4f178bbb71cf2ecfcd42accce558b27199ab4fb59cb78f2483fe21ef36d9.vk": {
    "cid": "QmfCeddjFpWtavzfEzZpJfzSajGNwfL4RjFXWAvA9TSnTV",
    "digest": "4dae975de4f011f101f5a2f86d1daaba",
    "sector_size": 536870912
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-0-0-3ea05428c9d11689f23529cde32fd30aabd50f7d2c93657c1d3650bca3e8ea9e.vk": {
    "cid": "QmSTCXF2ipGA3f6muVo6kHc2URSx6PzZxGUqu7uykaH5KU",
    "digest": "ffd79788d614d27919ae5bd2d94eacb6",
    "sector_size": 2048
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-0-0-50c7368dea9593ed0989e70974d28024efa9d156d585b7eea1be22b2e753f331.vk": {
    "cid": "QmbmUMa3TbbW3X5kFhExs6WgC4KeWT18YivaVmXDkB6ANG",
    "digest": "79ebb55f56fda427743e35053edad8fc",
    "sector_size": 8388608
  }
}
`

const srs = `{}`

func TestGetParams(t *testing.T) {
	pd := t.TempDir()
	require.NoError(t, os.Setenv(dirEnv, pd))

	err := GetParams(context.Background(), []byte(params), []byte(srs), 0)
	require.NoError(t, err)
}

func TestGetParamsParallel(t *testing.T) {
	pd := t.TempDir()
	require.NoError(t, os.Setenv(dirEnv, pd))

	eg, ctx := errgroup.WithContext(context.Background())

	for i := 0; i < 4; i++ {
		eg.Go(func() error { return GetParams(ctx, []byte(params), []byte(srs), 0) })
	}

	require.NoError(t, eg.Wait())
}

func TestCheckFileIgnoresUntrustableExtension(t *testing.T) {
	const mockParamInfoBytes = `{
			"cid": "Qmxxxxxdoesntexist",
			"digest": "0e0958009936b9d5e515ec97b8cb792d",
			"sector_size": 2048
	}`
	var mockParamInfo paramFile
	if err := json.Unmarshal([]byte(mockParamInfoBytes), &mockParamInfo); err != nil {
		require.NoError(t, err)
	}
	ft := &fetch{}

	err := os.Setenv("TRUST_PARAMS", "1")
	require.NoError(t, err)
	defer func() {
		err := os.Unsetenv("TRUST_PARAMS")
		assert.NoError(t, err)
	}()

	err = ft.checkFile(filepath.Join(".", "should_ignore.params"), mockParamInfo)
	assert.NoError(t, err)

	err = ft.checkFile(filepath.Join(".", "should_check_and_fail.vk"), mockParamInfo)
	assert.Error(t, err)

	err = ft.checkFile(filepath.Join(".", "also_check_and_fail.srs"), mockParamInfo)
	assert.Error(t, err)
}

func TestDoFetchBadStatusCode(t *testing.T) {
	cases := []struct {
		name string
		code int
	}{
		{"BadRequest", http.StatusBadRequest},
		{"Forbidden", http.StatusForbidden},
		{"NotFound", http.StatusNotFound},
		{"InternalServerError", http.StatusInternalServerError},
		{"BadGateway", http.StatusBadGateway},
		{"ServiceUnavailable", http.StatusServiceUnavailable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
			}))
			defer ts.Close()

			t.Setenv("IPFS_GATEWAY", ts.URL+"/ipfs/")

			tmpDir := t.TempDir()
			outPath := filepath.Join(tmpDir, "test-param-file")

			info := paramFile{
				Cid:    "QmFakeCid",
				Digest: "0000000000000000",
			}

			ft := &fetch{}
			err := ft.doFetch(context.Background(), outPath, info)
			require.Error(t, err)

			errMsg := fmt.Sprintf("%v", err)
			assert.True(t, strings.Contains(errMsg, fmt.Sprintf("%d", tc.code)),
				"error should contain HTTP status code %d, got: %s", tc.code, errMsg)
			assert.False(t, strings.Contains(errMsg, "checksum"),
				"error should not mention checksum mismatch, got: %s", errMsg)
		})
	}
}

// withGateways points the fetcher at the given sources for the test's duration.
// It swaps package state, so tests using it must not run in parallel.
func withGateways(t *testing.T, gws ...string) {
	t.Helper()

	t.Setenv("IPFS_GATEWAY", "")

	old := defaultGateways
	defaultGateways = gws
	t.Cleanup(func() { defaultGateways = old })
}

func TestDoFetchGatewaySelection(t *testing.T) {
	const (
		body   = "hello params"
		digest = "3c71fb15f04da469c7eb6abcc72afaef" // blake2b-512(body)[:16], as in parameters.json
	)

	cases := []struct {
		name        string
		first       http.HandlerFunc
		reachSecond bool
	}{
		{
			name: "http error falls back",
			first: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			},
			reachSecond: true,
		},
		{
			name: "wrong content falls back",
			first: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "garbage from a broken mirror")
			},
			reachSecond: true,
		},
		{
			name: "healthy gateway retires the rest",
			first: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			},
			reachSecond: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var firstHits, secondHits atomic.Int32

			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				firstHits.Add(1)
				tc.first(w, r)
			}))
			defer first.Close()

			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				secondHits.Add(1)
				// Content the first gateway served must not become this one's
				// resume offset.
				assert.Equal(t, "bytes=0-", r.Header.Get("Range"))
				_, _ = io.WriteString(w, body)
			}))
			defer second.Close()

			withGateways(t, first.URL+"/ipfs/", second.URL+"/ipfs/")

			ft := &fetch{}
			out := filepath.Join(t.TempDir(), "test-param-file")
			require.NoError(t, ft.doFetch(context.Background(), out, paramFile{Cid: "QmFakeCid", Digest: digest}))

			got, err := os.ReadFile(out)
			require.NoError(t, err)
			assert.Equal(t, body, string(got))

			assert.Equal(t, int32(1), firstHits.Load(), "a full response must not be retried")
			if tc.reachSecond {
				assert.NotZero(t, secondHits.Load(), "the second gateway must serve the file")
			} else {
				assert.Zero(t, secondHits.Load(), "a healthy gateway must retire the rest")
			}
		})
	}
}

func TestDoFetchAllGatewaysFail(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer second.Close()

	withGateways(t, first.URL+"/ipfs/", second.URL+"/ipfs/")

	ft := &fetch{}
	out := filepath.Join(t.TempDir(), "test-param-file")
	err := ft.doFetch(context.Background(), out, paramFile{Cid: "QmFakeCid"})
	require.Error(t, err)

	// Every source tried is named in the error with the status it answered, so
	// a broken mirror is identifiable.
	errMsg := err.Error()
	assert.Contains(t, errMsg, first.URL)
	assert.Contains(t, errMsg, "404")
	assert.Contains(t, errMsg, second.URL)
	assert.Contains(t, errMsg, "500")
}

func TestDoFetchExplicitGatewayIsNotSupplemented(t *testing.T) {
	var defaultHits atomic.Int32

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defaultHits.Add(1)
		_, _ = io.WriteString(w, "should not be reached")
	}))
	defer fallback.Close()

	explicit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer explicit.Close()

	withGateways(t, fallback.URL+"/ipfs/")
	t.Setenv("IPFS_GATEWAY", explicit.URL+"/ipfs/")

	ft := &fetch{}
	out := filepath.Join(t.TempDir(), "test-param-file")
	err := ft.doFetch(context.Background(), out, paramFile{Cid: "QmFakeCid"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), explicit.URL)

	assert.Zero(t, defaultHits.Load(), "an explicit gateway must not fall back to the defaults")
}

func TestFetchFromGatewayRestartsWhenRangeIgnored(t *testing.T) {
	const (
		partial = "partial"
		body    = "the complete parameter file"
	)

	// A gateway that ignores Range and always answers 200 with the whole body.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, fmt.Sprintf("bytes=%d-", len(partial)), r.Header.Get("Range"),
			"the fetcher should have asked to resume")
		_, _ = io.WriteString(w, body)
	}))
	defer ts.Close()

	out := filepath.Join(t.TempDir(), "test-param-file")
	require.NoError(t, os.WriteFile(out, []byte(partial), 0666))

	ft := &fetch{}
	err := ft.fetchFromGateway(context.Background(), out, ts.URL+"/ipfs/", paramFile{Cid: "QmFakeCid", Digest: "771996270702cc3513e0b2964291809a"})
	require.NoError(t, err)

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, body, string(got), "partial bytes must be discarded, not appended to")
}

func TestFetchFromGatewayDiscardsUnresumableFile(t *testing.T) {
	const body = "the complete parameter file"

	// A well-behaved gateway: a resume offset at or past the end is a 416.
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Range") != "bytes=0-" {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer ts.Close()

	// A local file at least as long as the real one, but wrong: no gateway can
	// serve a resume offset for it, so it would otherwise fail every run.
	out := filepath.Join(t.TempDir(), "test-param-file")
	require.NoError(t, os.WriteFile(out, []byte(strings.Repeat("x", len(body)+10)), 0666))

	ft := &fetch{}
	err := ft.fetchFromGateway(context.Background(), out, ts.URL+"/ipfs/", paramFile{Cid: "QmFakeCid", Digest: "771996270702cc3513e0b2964291809a"})
	require.NoError(t, err)

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, body, string(got), "an unresumable file must be discarded and refetched")
	assert.Equal(t, int32(2), hits.Load(), "one rejected resume, then one full fetch")
}

// Exercise the public entry point so checksum failures cannot bypass recovery.
func TestGetParamsResumeRecovery(t *testing.T) {
	const body = "hello params"
	const digest = "3c71fb15f04da469c7eb6abcc72afaef"
	cases := []struct {
		name, local                                  string
		ignoreRange, rejectRange, badBody, wantError bool
		wantRanges                                   []string
	}{
		{name: "corrupt prefix", local: "xxxxx", wantRanges: []string{"bytes=5-", "bytes=0-"}},
		{name: "valid prefix", local: "hello", wantRanges: []string{"bytes=5-"}},
		{name: "bad full response", badBody: true, wantError: true, wantRanges: []string{"bytes=0-"}},
		{name: "ignored range with bad body", local: "xxxxx", ignoreRange: true, badBody: true, wantError: true, wantRanges: []string{"bytes=5-"}},
		{name: "rejected range with bad body", local: "xxxxx", rejectRange: true, badBody: true, wantError: true, wantRanges: []string{"bytes=5-", "bytes=0-"}},
		{name: "bad resumed and full responses", local: "xxxxx", badBody: true, wantError: true, wantRanges: []string{"bytes=5-", "bytes=0-"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var ranges []string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rng := r.Header.Get("Range")
				mu.Lock()
				ranges = append(ranges, rng)
				mu.Unlock()
				content := body
				if tc.badBody {
					content = "wrong params"
				}
				if rng == "bytes=5-" && tc.rejectRange {
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return
				}
				if rng == "bytes=5-" && !tc.ignoreRange {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 5-%d/%d", len(content)-1, len(content)))
					w.WriteHeader(http.StatusPartialContent)
					content = content[5:]
				}
				_, _ = io.WriteString(w, content)
			}))
			defer ts.Close()
			t.Setenv("IPFS_GATEWAY", ts.URL+"/")
			t.Setenv("TRUST_PARAMS", "")
			t.Setenv(dirEnv, t.TempDir())
			out := filepath.Join(getParamDir(), "test.vk")
			if tc.local != "" {
				require.NoError(t, os.WriteFile(out, []byte(tc.local), 0600))
			}
			manifest := fmt.Sprintf(`{"test.vk":{"cid":"QmFakeCid","digest":%q}}`, digest)
			err := GetParams(context.Background(), []byte(manifest), []byte("{}"), 0)
			if tc.wantError {
				require.Error(t, err)
				_, statErr := os.Stat(out)
				require.True(t, os.IsNotExist(statErr), "invalid content must be removed")
			} else {
				require.NoError(t, err)
				got, err := os.ReadFile(out)
				require.NoError(t, err)
				assert.Equal(t, body, string(got))
			}
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tc.wantRanges, ranges)
		})
	}
}
