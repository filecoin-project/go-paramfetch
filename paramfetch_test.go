package paramfetch

import (
	"context"
	"io/ioutil"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-0-0-5294475db5237a2e83c3e52fd6c2b03859a1831d45ed08c4f35dbf9a803165a9.vk": {
    "cid": "QmUiVYCQUgr6Y13pZFr8acWpSM4xvTXUdcvGmxyuHbKhsc",
    "digest": "34d4feeacd9abf788d69ef1bb4d8fd00",
    "sector_size": 8388608
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-0-0-7d739b8cf60f1b0709eeebee7730e297683552e4b69cab6984ec0285663c5781.vk": {
    "cid": "QmfA31fbCWojSmhSGvvfxmxaYCpMoXP95zEQ9sLvBGHNaN",
    "digest": "bd2cd62f65c1ab84f19ca27e97b7c731",
    "sector_size": 536870912
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-8-0-0377ded656c6f524f1618760bffe4e0a1c51d5a70c4509eedae8a27555733edc.vk": {
    "cid": "QmR9i9KL3vhhAqTBGj1bPPC7LvkptxrH9RvxJxLN1vvsBE",
    "digest": "0f8ec542485568fa3468c066e9fed82b",
    "sector_size": 34359738368
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-8-0-559e581f022bb4e4ec6e719e563bf0e026ad6de42e56c18714a2c692b1b88d7e.vk": {
    "cid": "QmZCvxKcKP97vDAk8Nxs9R1fWtqpjQrAhhfXPoCi1nkDoF",
    "digest": "fc02943678dd119e69e7fab8420e8819",
    "sector_size": 34359738368
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-8-2-2627e4006b67f99cef990c0a47d5426cb7ab0a0ad58fc1061547bf2d28b09def.vk": {
    "cid": "QmWV8rqZLxs1oQN9jxNWmnT1YdgLwCcscv94VARrhHf1T7",
    "digest": "59d2bf1857adc59a4f08fcf2afaa916b",
    "sector_size": 68719476736
  },
  "v28-proof-of-spacetime-fallback-merkletree-poseidon_hasher-8-8-2-b62098629d07946e9028127e70295ed996fe3ed25b0f9f88eb610a0ab4385a3c.vk": {
    "cid": "QmbfQjPD7EpzjhWGmvWAsyN2mAZ4PcYhsf3ujuhU9CSuBm",
    "digest": "6d3789148fb6466d07ee1e24d6292fd6",
    "sector_size": 68719476736
  },
  "v28-stacked-proof-of-replication-merkletree-poseidon_hasher-8-0-0-sha256_hasher-032d3138d22506ec0082ed72b2dcba18df18477904e35bafee82b3793b06832f.vk": {
    "cid": "QmamahpFCstMUqHi2qGtVoDnRrsXhid86qsfvoyCTKJqHr",
    "digest": "dc1ade9929ade1708238f155343044ac",
    "sector_size": 2048
  },
  "v28-stacked-proof-of-replication-merkletree-poseidon_hasher-8-0-0-sha256_hasher-6babf46ce344ae495d558e7770a585b2382d54f225af8ed0397b8be7c3fcd472.vk": {
    "cid": "QmWionkqH2B6TXivzBSQeSyBxojaiAFbzhjtwYRrfwd8nH",
    "digest": "065179da19fbe515507267677f02823e",
    "sector_size": 536870912
  },
  "v28-stacked-proof-of-replication-merkletree-poseidon_hasher-8-0-0-sha256_hasher-ecd683648512ab1765faa2a5f14bab48f676e633467f0aa8aad4b55dcb0652bb.vk": {
    "cid": "QmYCuipFyvVW1GojdMrjK1JnMobXtT4zRCZs1CGxjizs99",
    "digest": "b687beb9adbd9dabe265a7e3620813e4",
    "sector_size": 8388608
  },
  "v28-stacked-proof-of-replication-merkletree-poseidon_hasher-8-8-0-sha256_hasher-82a357d2f2ca81dc61bb45f4a762807aedee1b0a53fd6c4e77b46a01bfef7820.vk": {
    "cid": "Qmf93EMrADXAK6CyiSfE8xx45fkMfR3uzKEPCvZC1n2kzb",
    "digest": "0c7b4aac1c40fdb7eb82bc355b41addf",
    "sector_size": 34359738368
  },
  "v28-stacked-proof-of-replication-merkletree-poseidon_hasher-8-8-2-sha256_hasher-96f1b4a04c5c51e4759bbf224bbc2ef5a42c7100f16ec0637123f16a845ddfb2.vk": {
    "cid": "QmehSmC6BhrgRZakPDta2ewoH9nosNzdjCqQRXsNFNUkLN",
    "digest": "a89884252c04c298d0b3c81bfd884164",
    "sector_size": 68719476736
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
