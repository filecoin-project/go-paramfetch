package paramfetch

import (
	"context"
	"io/ioutil"
	"os"
	"sync"
	"testing"

	logging "github.com/ipfs/go-log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	pd, err := ioutil.TempDir("", "paramfetch-test-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(pd))
	})
	require.NoError(t, os.Setenv(dirEnv, pd))

	ctx := context.TODO()

	err = GetParams(ctx, []byte(params), []byte(srs), 0)
	require.NoError(t, err)
}

func TestGetParamsParallel(t *testing.T) {
	pd, err := ioutil.TempDir("", "paramfetch-test-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(pd))
	})
	require.NoError(t, os.Setenv(dirEnv, pd))

	ctx := context.TODO()

	n := 4

	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()

			err := GetParams(ctx, []byte(params), []byte(srs), 0)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
}
