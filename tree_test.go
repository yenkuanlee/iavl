// nolint:errcheck
package iavl

import (
	"bytes"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	db "github.com/tendermint/tm-db"

	iavlrand "github.com/cosmos/iavl/internal/rand"
)

var testLevelDB bool
var testFuzzIterations int
var random *iavlrand.Rand

func SetupTest() {
	random = iavlrand.NewRand()
	random.Seed(0) // for determinism
	flag.BoolVar(&testLevelDB, "test.leveldb", false, "test leveldb backend")
	flag.IntVar(&testFuzzIterations, "test.fuzz-iterations", 100000, "number of fuzz testing iterations")
	flag.Parse()
}

func getTestDB() (db.DB, func()) {
	if testLevelDB {
		d, err := db.NewGoLevelDB("test", ".")
		if err != nil {
			panic(err)
		}
		return d, func() {
			d.Close()
			os.RemoveAll("./test.db")
		}
	}
	return db.NewMemDB(), func() {}
}

func TestVersionedRandomTree(t *testing.T) {
	require := require.New(t)
	SetupTest()
	d, closeDB := getTestDB()
	defer closeDB()

	tree, err := NewMutableTree(d, 100)
	require.NoError(err)
	versions := 50
	keysPerVersion := 30

	// Create a tree of size 1000 with 100 versions.
	for i := 1; i <= versions; i++ {
		for j := 0; j < keysPerVersion; j++ {
			k := []byte(iavlrand.RandStr(8))
			v := []byte(iavlrand.RandStr(8))
			tree.Set(k, v)
		}
		tree.SaveVersion()
	}
	roots, err := tree.ndb.getRoots()
	require.NoError(err)

	require.Equal(versions, len(roots), "wrong number of roots")
	require.Equal(versions*keysPerVersion, len(tree.ndb.leafNodes()), "wrong number of nodes")

	// Before deleting old versions, we should have equal or more nodes in the
	// db than in the current tree version.
	require.True(len(tree.ndb.nodes()) >= tree.nodeSize())

	// Ensure it returns all versions in sorted order
	available := tree.AvailableVersions()
	assert.Equal(t, versions, len(available))
	assert.Equal(t, 1, available[0])
	assert.Equal(t, versions, available[len(available)-1])

	for i := 1; i < versions; i++ {
		tree.DeleteVersion(int64(i))
	}

	require.Len(tree.versions, 1, "tree must have one version left")
	tr, err := tree.GetImmutable(int64(versions))
	require.NoError(err, "GetImmutable should not error for version %d", versions)
	require.Equal(tr.root, tree.root)

	// we should only have one available version now
	available = tree.AvailableVersions()
	assert.Equal(t, 1, len(available))
	assert.Equal(t, versions, available[0])

	// After cleaning up all previous versions, we should have as many nodes
	// in the db as in the current tree version.
	require.Len(tree.ndb.leafNodes(), int(tree.Size()))

	require.Equal(tree.nodeSize(), len(tree.ndb.nodes()))
}

// nolint: dupl
func TestTreeHash(t *testing.T) {
	const (
		randSeed  = 49872768940 // For deterministic tests
		keySize   = 16
		valueSize = 16

		versions    = 4    // number of versions to generate
		versionOps  = 4096 // number of operations (create/update/delete) per version
		updateRatio = 0.4  // ratio of updates out of all operations
		deleteRatio = 0.2  // ratio of deletes out of all operations
	)

	// expected hashes for each version
	expectHashes := []string{
		"58ec30fa27f338057e5964ed9ec3367e59b2b54bec4c194f10fde7fed16c2a1c",
		"91ad3ace227372f0064b2d63e8493ce8f4bdcbd16c7a8e4f4d54029c9db9570c",
		"92c25dce822c5968c228cfe7e686129ea281f79273d4a8fcf6f9130a47aa5421",
		"e44d170925554f42e00263155c19574837a38e3efed8910daccc7fa12f560fa0",
	}
	require.Len(t, expectHashes, versions, "must have expected hashes for all versions")

	r := rand.New(rand.NewSource(randSeed))
	tree, err := NewMutableTree(db.NewMemDB(), 0)
	require.NoError(t, err)

	keys := make([][]byte, 0, versionOps)
	for i := 0; i < versions; i++ {
		for j := 0; j < versionOps; j++ {
			key := make([]byte, keySize)
			value := make([]byte, valueSize)

			// The performance of this is likely to be terrible, but that's fine for small tests
			switch {
			case len(keys) > 0 && r.Float64() <= deleteRatio:
				index := r.Intn(len(keys))
				key = keys[index]
				keys = append(keys[:index], keys[index+1:]...)
				_, removed := tree.Remove(key)
				require.True(t, removed)

			case len(keys) > 0 && r.Float64() <= updateRatio:
				key = keys[r.Intn(len(keys))]
				r.Read(value)
				updated := tree.Set(key, value)
				require.True(t, updated)

			default:
				r.Read(key)
				r.Read(value)
				// If we get an update, set again
				for tree.Set(key, value) {
					key = make([]byte, keySize)
					r.Read(key)
				}
				keys = append(keys, key)
			}
		}
		hash, version, err := tree.SaveVersion()
		require.NoError(t, err)
		require.EqualValues(t, i+1, version)
		require.Equal(t, expectHashes[i], hex.EncodeToString(hash))
	}

	require.EqualValues(t, versions, tree.Version())
}

func TestVersionedRandomTreeSmallKeys(t *testing.T) {
	require := require.New(t)
	d, closeDB := getTestDB()
	defer closeDB()

	tree, err := NewMutableTree(d, 100)
	require.NoError(err)
	singleVersionTree, err := getTestTree(0)
	require.NoError(err)
	versions := 20
	keysPerVersion := 50

	for i := 1; i <= versions; i++ {
		for j := 0; j < keysPerVersion; j++ {
			// Keys of size one are likely to be overwritten.
			k := []byte(iavlrand.RandStr(1))
			v := []byte(iavlrand.RandStr(8))
			tree.Set(k, v)
			singleVersionTree.Set(k, v)
		}
		tree.SaveVersion()
	}
	singleVersionTree.SaveVersion()

	for i := 1; i < versions; i++ {
		tree.DeleteVersion(int64(i))
	}

	// After cleaning up all previous versions, we should have as many nodes
	// in the db as in the current tree version. The simple tree must be equal
	// too.
	require.Len(tree.ndb.leafNodes(), int(tree.Size()))
	require.Len(tree.ndb.nodes(), tree.nodeSize())
	require.Len(tree.ndb.nodes(), singleVersionTree.nodeSize())

	// Try getting random keys.
	for i := 0; i < keysPerVersion; i++ {
		_, val := tree.Get([]byte(iavlrand.RandStr(1)))
		require.NotNil(val)
		require.NotEmpty(val)
	}
}

func TestVersionedRandomTreeSmallKeysRandomDeletes(t *testing.T) {
	require := require.New(t)
	d, closeDB := getTestDB()
	defer closeDB()

	tree, err := NewMutableTree(d, 100)
	require.NoError(err)
	singleVersionTree, err := getTestTree(0)
	require.NoError(err)
	versions := 30
	keysPerVersion := 50

	for i := 1; i <= versions; i++ {
		for j := 0; j < keysPerVersion; j++ {
			// Keys of size one are likely to be overwritten.
			k := []byte(iavlrand.RandStr(1))
			v := []byte(iavlrand.RandStr(8))
			tree.Set(k, v)
			singleVersionTree.Set(k, v)
		}
		tree.SaveVersion()
	}
	singleVersionTree.SaveVersion()

	for _, i := range iavlrand.RandPerm(versions - 1) {
		tree.DeleteVersion(int64(i + 1))
	}

	// After cleaning up all previous versions, we should have as many nodes
	// in the db as in the current tree version. The simple tree must be equal
	// too.
	require.Len(tree.ndb.leafNodes(), int(tree.Size()))
	require.Len(tree.ndb.nodes(), tree.nodeSize())
	require.Len(tree.ndb.nodes(), singleVersionTree.nodeSize())

	// Try getting random keys.
	for i := 0; i < keysPerVersion; i++ {
		_, val := tree.Get([]byte(iavlrand.RandStr(1)))
		require.NotNil(val)
		require.NotEmpty(val)
	}
}

func TestVersionedTreeSpecial1(t *testing.T) {
	tree, err := getTestTree(100)
	require.NoError(t, err)

	tree.Set([]byte("C"), []byte("so43QQFN"))
	tree.SaveVersion()

	tree.Set([]byte("A"), []byte("ut7sTTAO"))
	tree.SaveVersion()

	tree.Set([]byte("X"), []byte("AoWWC1kN"))
	tree.SaveVersion()

	tree.Set([]byte("T"), []byte("MhkWjkVy"))
	tree.SaveVersion()

	tree.DeleteVersion(1)
	tree.DeleteVersion(2)
	tree.DeleteVersion(3)

	require.Equal(t, tree.nodeSize(), len(tree.ndb.nodes()))
}

func TestVersionedRandomTreeSpecial2(t *testing.T) {
	require := require.New(t)
	tree, err := getTestTree(100)
	require.NoError(err)

	tree.Set([]byte("OFMe2Yvm"), []byte("ez2OtQtE"))
	tree.Set([]byte("WEN4iN7Y"), []byte("kQNyUalI"))
	tree.SaveVersion()

	tree.Set([]byte("1yY3pXHr"), []byte("udYznpII"))
	tree.Set([]byte("7OSHNE7k"), []byte("ff181M2d"))
	tree.SaveVersion()

	tree.DeleteVersion(1)
	require.Len(tree.ndb.nodes(), tree.nodeSize())
}

func TestVersionedEmptyTree(t *testing.T) {
	require := require.New(t)
	d, closeDB := getTestDB()
	defer closeDB()

	tree, err := NewMutableTree(d, 0)
	require.NoError(err)

	hash, v, err := tree.SaveVersion()
	require.NoError(err)
	require.Equal("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hex.EncodeToString(hash))
	require.EqualValues(1, v)

	hash, v, err = tree.SaveVersion()
	require.NoError(err)
	require.Equal("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hex.EncodeToString(hash))
	require.EqualValues(2, v)

	hash, v, err = tree.SaveVersion()
	require.NoError(err)
	require.Equal("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hex.EncodeToString(hash))
	require.EqualValues(3, v)

	hash, v, err = tree.SaveVersion()
	require.NoError(err)
	require.Equal("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hex.EncodeToString(hash))
	require.EqualValues(4, v)

	require.EqualValues(4, tree.Version())

	require.True(tree.VersionExists(1))
	require.True(tree.VersionExists(3))

	require.NoError(tree.DeleteVersion(1))
	require.NoError(tree.DeleteVersion(3))

	require.False(tree.VersionExists(1))
	require.False(tree.VersionExists(3))

	tree.Set([]byte("k"), []byte("v"))
	require.EqualValues(5, tree.root.version)

	// Now reload the tree.

	tree, err = NewMutableTree(d, 0)
	require.NoError(err)
	tree.Load()

	require.False(tree.VersionExists(1))
	require.True(tree.VersionExists(2))
	require.False(tree.VersionExists(3))

	t2, err := tree.GetImmutable(2)
	require.NoError(err, "GetImmutable should not fail for version 2")

	require.Empty(t2.root)
}

func TestVersionedTree(t *testing.T) {
	require := require.New(t)
	d, closeDB := getTestDB()
	defer closeDB()

	tree, err := NewMutableTree(d, 0)
	require.NoError(err)

	// We start with zero keys in the databse.
	require.Equal(0, tree.ndb.size())
	require.True(tree.IsEmpty())

	// version 0

	tree.Set([]byte("key1"), []byte("val0"))
	tree.Set([]byte("key2"), []byte("val0"))

	// Still zero keys, since we haven't written them.
	require.Len(tree.ndb.leafNodes(), 0)
	require.False(tree.IsEmpty())

	// Now let's write the keys to storage.
	hash1, v, err := tree.SaveVersion()
	require.NoError(err)
	require.False(tree.IsEmpty())
	require.EqualValues(1, v)

	// -----1-----
	// key1 = val0  version=1
	// key2 = val0  version=1
	// key2 (root)  version=1
	// -----------

	nodes1 := tree.ndb.leafNodes()
	require.Len(nodes1, 2, "db should have a size of 2")

	// version  1

	tree.Set([]byte("key1"), []byte("val1"))
	tree.Set([]byte("key2"), []byte("val1"))
	tree.Set([]byte("key3"), []byte("val1"))
	require.Len(tree.ndb.leafNodes(), len(nodes1))

	hash2, v2, err := tree.SaveVersion()
	require.NoError(err)
	require.False(bytes.Equal(hash1, hash2))
	require.EqualValues(v+1, v2)

	// Recreate a new tree and load it, to make sure it works in this
	// scenario.
	tree, err = NewMutableTree(d, 100)
	require.NoError(err)
	_, err = tree.Load()
	require.NoError(err)

	require.Len(tree.versions, 2, "wrong number of versions")
	require.EqualValues(v2, tree.Version())

	// -----1-----
	// key1 = val0  <orphaned>
	// key2 = val0  <orphaned>
	// -----2-----
	// key1 = val1
	// key2 = val1
	// key3 = val1
	// -----------

	nodes2 := tree.ndb.leafNodes()
	require.Len(nodes2, 5, "db should have grown in size")
	require.Len(tree.ndb.orphans(), 3, "db should have three orphans")

	// Create three more orphans.
	tree.Remove([]byte("key1")) // orphans both leaf node and inner node containing "key1" and "key2"
	tree.Set([]byte("key2"), []byte("val2"))

	hash3, v3, _ := tree.SaveVersion()
	require.EqualValues(3, v3)

	// -----1-----
	// key1 = val0  <orphaned> (replaced)
	// key2 = val0  <orphaned> (replaced)
	// -----2-----
	// key1 = val1  <orphaned> (removed)
	// key2 = val1  <orphaned> (replaced)
	// key3 = val1
	// -----3-----
	// key2 = val2
	// -----------

	nodes3 := tree.ndb.leafNodes()
	require.Len(nodes3, 6, "wrong number of nodes")
	require.Len(tree.ndb.orphans(), 7, "wrong number of orphans")

	hash4, _, _ := tree.SaveVersion()
	require.EqualValues(hash3, hash4)
	require.NotNil(hash4)

	tree, err = NewMutableTree(d, 100)
	require.NoError(err)
	_, err = tree.Load()
	require.NoError(err)

	// ------------
	// DB UNCHANGED
	// ------------

	nodes4 := tree.ndb.leafNodes()
	require.Len(nodes4, len(nodes3), "db should not have changed in size")

	tree.Set([]byte("key1"), []byte("val0"))

	// "key2"
	_, val := tree.GetVersioned([]byte("key2"), 0)
	require.Nil(val)

	_, val = tree.GetVersioned([]byte("key2"), 1)
	require.Equal("val0", string(val))

	_, val = tree.GetVersioned([]byte("key2"), 2)
	require.Equal("val1", string(val))

	_, val = tree.Get([]byte("key2"))
	require.Equal("val2", string(val))

	// "key1"
	_, val = tree.GetVersioned([]byte("key1"), 1)
	require.Equal("val0", string(val))

	_, val = tree.GetVersioned([]byte("key1"), 2)
	require.Equal("val1", string(val))

	_, val = tree.GetVersioned([]byte("key1"), 3)
	require.Nil(val)

	_, val = tree.GetVersioned([]byte("key1"), 4)
	require.Nil(val)

	_, val = tree.Get([]byte("key1"))
	require.Equal("val0", string(val))

	// "key3"
	_, val = tree.GetVersioned([]byte("key3"), 0)
	require.Nil(val)

	_, val = tree.GetVersioned([]byte("key3"), 2)
	require.Equal("val1", string(val))

	_, val = tree.GetVersioned([]byte("key3"), 3)
	require.Equal("val1", string(val))

	// Delete a version. After this the keys in that version should not be found.

	tree.DeleteVersion(2)

	// -----1-----
	// key1 = val0
	// key2 = val0
	// -----2-----
	// key3 = val1
	// -----3-----
	// key2 = val2
	// -----------

	nodes5 := tree.ndb.leafNodes()
	require.True(len(nodes5) < len(nodes4), "db should have shrunk after delete %d !< %d", len(nodes5), len(nodes4))

	_, val = tree.GetVersioned([]byte("key2"), 2)
	require.Nil(val)

	_, val = tree.GetVersioned([]byte("key3"), 2)
	require.Nil(val)

	// But they should still exist in the latest version.

	_, val = tree.Get([]byte("key2"))
	require.Equal("val2", string(val))

	_, val = tree.Get([]byte("key3"))
	require.Equal("val1", string(val))

	// Version 1 should still be available.

	_, val = tree.GetVersioned([]byte("key1"), 1)
	require.Equal("val0", string(val))

	_, val = tree.GetVersioned([]byte("key2"), 1)
	require.Equal("val0", string(val))
}

func TestVersionedTreeVersionDeletingEfficiency(t *testing.T) {
	d, closeDB := getTestDB()
	defer closeDB()

	tree, err := NewMutableTree(d, 0)
	require.NoError(t, err)

	tree.Set([]byte("key0"), []byte("val0"))
	tree.Set([]byte("key1"), []byte("val0"))
	tree.Set([]byte("key2"), []byte("val0"))
	tree.SaveVersion()

	require.Len(t, tree.ndb.leafNodes(), 3)

	tree.Set([]byte("key1"), []byte("val1"))
	tree.Set([]byte("key2"), []byte("val1"))
	tree.Set([]byte("key3"), []byte("val1"))
	tree.SaveVersion()

	require.Len(t, tree.ndb.leafNodes(), 6)

	tree.Set([]byte("key0"), []byte("val2"))
	tree.Remove([]byte("key1"))
	tree.Set([]byte("key2"), []byte("val2"))
	tree.SaveVersion()

	require.Len(t, tree.ndb.leafNodes(), 8)

	tree.DeleteVersion(2)

	require.Len(t, tree.ndb.leafNodes(), 6)

	tree.DeleteVersion(1)

	require.Len(t, tree.ndb.leafNodes(), 3)

	tree2, err := getTestTree(0)
	require.NoError(t, err)
	tree2.Set([]byte("key0"), []byte("val2"))
	tree2.Set([]byte("key2"), []byte("val2"))
	tree2.Set([]byte("key3"), []byte("val1"))
	tree2.SaveVersion()

	require.Equal(t, tree2.nodeSize(), tree.nodeSize())
}

func TestVersionedTreeOrphanDeleting(t *testing.T) {
	tree, err := getTestTree(0)
	require.NoError(t, err)

	tree.Set([]byte("key0"), []byte("val0"))
	tree.Set([]byte("key1"), []byte("val0"))
	tree.Set([]byte("key2"), []byte("val0"))
	tree.SaveVersion()

	tree.Set([]byte("key1"), []byte("val1"))
	tree.Set([]byte("key2"), []byte("val1"))
	tree.Set([]byte("key3"), []byte("val1"))
	tree.SaveVersion()

	tree.Set([]byte("key0"), []byte("val2"))
	tree.Remove([]byte("key1"))
	tree.Set([]byte("key2"), []byte("val2"))
	tree.SaveVersion()

	tree.DeleteVersion(2)

	_, val := tree.Get([]byte("key0"))
	require.Equal(t, val, []byte("val2"))

	_, val = tree.Get([]byte("key1"))
	require.Nil(t, val)

	_, val = tree.Get([]byte("key2"))
	require.Equal(t, val, []byte("val2"))

	_, val = tree.Get([]byte("key3"))
	require.Equal(t, val, []byte("val1"))

	tree.DeleteVersion(1)

	require.Len(t, tree.ndb.leafNodes(), 3)
}

func TestVersionedTreeSpecialCase(t *testing.T) {
	require := require.New(t)
	d, closeDB := getTestDB()
	defer closeDB()

	tree, err := NewMutableTree(d, 0)
	require.NoError(err)

	tree.Set([]byte("key1"), []byte("val0"))
	tree.Set([]byte("key2"), []byte("val0"))
	tree.SaveVersion()

	tree.Set([]byte("key1"), []byte("val1"))
	tree.Set([]byte("key2"), []byte("val1"))
	tree.SaveVersion()

	tree.Set([]byte("key2"), []byte("val2"))
	tree.SaveVersion()

	tree.DeleteVersion(2)

	_, val := tree.GetVersioned([]byte("key2"), 1)
	require.Equal("val0", string(val))
}

func TestVersionedTreeSpecialCase2(t *testing.T) {
	require := require.New(t)

	d := db.NewMemDB()
	tree, err := NewMutableTree(d, 100)
	require.NoError(err)

	tree.Set([]byte("key1"), []byte("val0"))
	tree.Set([]byte("key2"), []byte("val0"))
	tree.SaveVersion()

	tree.Set([]byte("key1"), []byte("val1"))
	tree.Set([]byte("key2"), []byte("val1"))
	tree.SaveVersion()

	tree.Set([]byte("key2"), []byte("val2"))
	tree.SaveVersion()

	tree, err = NewMutableTree(d, 100)
	require.NoError(err)
	_, err = tree.Load()
	require.NoError(err)

	require.NoError(tree.DeleteVersion(2))

	_, val := tree.GetVersioned([]byte("key2"), 1)
	require.Equal("val0", string(val))
}

func TestVersionedTreeSpecialCase3(t *testing.T) {
	require := require.New(t)
	tree, err := getTestTree(0)
	require.NoError(err)

	tree.Set([]byte("m"), []byte("liWT0U6G"))
	tree.Set([]byte("G"), []byte("7PxRXwUA"))
	tree.SaveVersion()

	tree.Set([]byte("7"), []byte("XRLXgf8C"))
	tree.SaveVersion()

	tree.Set([]byte("r"), []byte("bBEmIXBU"))
	tree.SaveVersion()

	tree.Set([]byte("i"), []byte("kkIS35te"))
	tree.SaveVersion()

	tree.Set([]byte("k"), []byte("CpEnpzKJ"))
	tree.SaveVersion()

	tree.DeleteVersion(1)
	tree.DeleteVersion(2)
	tree.DeleteVersion(3)
	tree.DeleteVersion(4)

	require.Equal(tree.nodeSize(), len(tree.ndb.nodes()))
}

func TestVersionedTreeSaveAndLoad(t *testing.T) {
	require := require.New(t)
	d := db.NewMemDB()
	tree, err := NewMutableTree(d, 0)
	require.NoError(err)

	// Loading with an empty root is a no-op.
	tree.Load()

	tree.Set([]byte("C"), []byte("so43QQFN"))
	tree.SaveVersion()

	tree.Set([]byte("A"), []byte("ut7sTTAO"))
	tree.SaveVersion()

	tree.Set([]byte("X"), []byte("AoWWC1kN"))
	tree.SaveVersion()

	tree.SaveVersion()
	tree.SaveVersion()
	tree.SaveVersion()

	preHash := tree.Hash()
	require.NotNil(preHash)

	require.Equal(int64(6), tree.Version())

	// Reload the tree, to test that roots and orphans are properly loaded.
	ntree, err := NewMutableTree(d, 0)
	require.NoError(err)
	ntree.Load()

	require.False(ntree.IsEmpty())
	require.Equal(int64(6), ntree.Version())

	postHash := ntree.Hash()
	require.Equal(preHash, postHash)

	ntree.Set([]byte("T"), []byte("MhkWjkVy"))
	ntree.SaveVersion()

	ntree.DeleteVersion(6)
	ntree.DeleteVersion(5)
	ntree.DeleteVersion(1)
	ntree.DeleteVersion(2)
	ntree.DeleteVersion(4)
	ntree.DeleteVersion(3)

	require.False(ntree.IsEmpty())
	require.Equal(int64(4), ntree.Size())
	require.Len(ntree.ndb.nodes(), ntree.nodeSize())
}

func TestVersionedTreeErrors(t *testing.T) {
	require := require.New(t)
	tree, err := getTestTree(100)
	require.NoError(err)

	// Can't delete non-existent versions.
	require.Error(tree.DeleteVersion(1))
	require.Error(tree.DeleteVersion(99))

	tree.Set([]byte("key"), []byte("val"))

	// Saving with content is ok.
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	// Can't delete current version.
	require.Error(tree.DeleteVersion(1))

	// Trying to get a key from a version which doesn't exist.
	_, val := tree.GetVersioned([]byte("key"), 404)
	require.Nil(val)

	// Same thing with proof. We get an error because a proof couldn't be
	// constructed.
	val, proof, err := tree.GetVersionedWithProof([]byte("key"), 404)
	require.Nil(val)
	require.Empty(proof)
	require.Error(err)
}

func TestVersionedCheckpoints(t *testing.T) {
	require := require.New(t)
	d, closeDB := getTestDB()
	defer closeDB()

	tree, err := NewMutableTree(d, 100)
	require.NoError(err)
	versions := 50
	keysPerVersion := 10
	versionsPerCheckpoint := 5
	keys := map[int64]([][]byte){}

	for i := 1; i <= versions; i++ {
		for j := 0; j < keysPerVersion; j++ {
			k := []byte(iavlrand.RandStr(1))
			v := []byte(iavlrand.RandStr(8))
			keys[int64(i)] = append(keys[int64(i)], k)
			tree.Set(k, v)
		}
		tree.SaveVersion()
	}

	for i := 1; i <= versions; i++ {
		if i%versionsPerCheckpoint != 0 {
			tree.DeleteVersion(int64(i))
		}
	}

	// Make sure all keys exist at least once.
	for _, ks := range keys {
		for _, k := range ks {
			_, val := tree.Get(k)
			require.NotEmpty(val)
		}
	}

	// Make sure all keys from deleted versions aren't present.
	for i := 1; i <= versions; i++ {
		if i%versionsPerCheckpoint != 0 {
			for _, k := range keys[int64(i)] {
				_, val := tree.GetVersioned(k, int64(i))
				require.Nil(val)
			}
		}
	}

	// Make sure all keys exist at all checkpoints.
	for i := 1; i <= versions; i++ {
		for _, k := range keys[int64(i)] {
			if i%versionsPerCheckpoint == 0 {
				_, val := tree.GetVersioned(k, int64(i))
				require.NotEmpty(val)
			}
		}
	}
}

func TestVersionedCheckpointsSpecialCase(t *testing.T) {
	require := require.New(t)
	tree, err := getTestTree(0)
	require.NoError(err)
	key := []byte("k")

	tree.Set(key, []byte("val1"))

	tree.SaveVersion()
	// ...
	tree.SaveVersion()
	// ...
	tree.SaveVersion()
	// ...
	// This orphans "k" at version 1.
	tree.Set(key, []byte("val2"))
	tree.SaveVersion()

	// When version 1 is deleted, the orphans should move to the next
	// checkpoint, which is version 10.
	tree.DeleteVersion(1)

	_, val := tree.GetVersioned(key, 2)
	require.NotEmpty(val)
	require.Equal([]byte("val1"), val)
}

func TestVersionedCheckpointsSpecialCase2(t *testing.T) {
	tree, err := getTestTree(0)
	require.NoError(t, err)

	tree.Set([]byte("U"), []byte("XamDUtiJ"))
	tree.Set([]byte("A"), []byte("UkZBuYIU"))
	tree.Set([]byte("H"), []byte("7a9En4uw"))
	tree.Set([]byte("V"), []byte("5HXU3pSI"))
	tree.SaveVersion()

	tree.Set([]byte("U"), []byte("Replaced"))
	tree.Set([]byte("A"), []byte("Replaced"))
	tree.SaveVersion()

	tree.Set([]byte("X"), []byte("New"))
	tree.SaveVersion()

	tree.DeleteVersion(1)
	tree.DeleteVersion(2)
}

func TestVersionedCheckpointsSpecialCase3(t *testing.T) {
	tree, err := getTestTree(0)
	require.NoError(t, err)

	tree.Set([]byte("n"), []byte("2wUCUs8q"))
	tree.Set([]byte("l"), []byte("WQ7mvMbc"))
	tree.SaveVersion()

	tree.Set([]byte("N"), []byte("ved29IqU"))
	tree.Set([]byte("v"), []byte("01jquVXU"))
	tree.SaveVersion()

	tree.Set([]byte("l"), []byte("bhIpltPM"))
	tree.Set([]byte("B"), []byte("rj97IKZh"))
	tree.SaveVersion()

	tree.DeleteVersion(2)

	tree.GetVersioned([]byte("m"), 1)
}

func TestVersionedCheckpointsSpecialCase4(t *testing.T) {
	tree, err := NewMutableTree(db.NewMemDB(), 0)
	require.NoError(t, err)

	tree.Set([]byte("U"), []byte("XamDUtiJ"))
	tree.Set([]byte("A"), []byte("UkZBuYIU"))
	tree.Set([]byte("H"), []byte("7a9En4uw"))
	tree.Set([]byte("V"), []byte("5HXU3pSI"))
	tree.SaveVersion()

	tree.Remove([]byte("U"))
	tree.Remove([]byte("A"))
	tree.SaveVersion()

	tree.Set([]byte("X"), []byte("New"))
	tree.SaveVersion()

	_, val := tree.GetVersioned([]byte("A"), 2)
	require.Nil(t, val)

	_, val = tree.GetVersioned([]byte("A"), 1)
	require.NotEmpty(t, val)

	tree.DeleteVersion(1)
	tree.DeleteVersion(2)

	_, val = tree.GetVersioned([]byte("A"), 2)
	require.Nil(t, val)

	_, val = tree.GetVersioned([]byte("A"), 1)
	require.Nil(t, val)
}

func TestVersionedCheckpointsSpecialCase5(t *testing.T) {
	tree, err := getTestTree(0)
	require.NoError(t, err)

	tree.Set([]byte("R"), []byte("ygZlIzeW"))
	tree.SaveVersion()

	tree.Set([]byte("j"), []byte("ZgmCWyo2"))
	tree.SaveVersion()

	tree.Set([]byte("R"), []byte("vQDaoz6Z"))
	tree.SaveVersion()

	tree.DeleteVersion(1)

	tree.GetVersioned([]byte("R"), 2)
}

func TestVersionedCheckpointsSpecialCase6(t *testing.T) {
	tree, err := getTestTree(0)
	require.NoError(t, err)

	tree.Set([]byte("Y"), []byte("MW79JQeV"))
	tree.Set([]byte("7"), []byte("Kp0ToUJB"))
	tree.Set([]byte("Z"), []byte("I26B1jPG"))
	tree.Set([]byte("6"), []byte("ZG0iXq3h"))
	tree.Set([]byte("2"), []byte("WOR27LdW"))
	tree.Set([]byte("4"), []byte("MKMvc6cn"))
	tree.SaveVersion()

	tree.Set([]byte("1"), []byte("208dOu40"))
	tree.Set([]byte("G"), []byte("7isI9OQH"))
	tree.Set([]byte("8"), []byte("zMC1YwpH"))
	tree.SaveVersion()

	tree.Set([]byte("7"), []byte("bn62vWbq"))
	tree.Set([]byte("5"), []byte("wZuLGDkZ"))
	tree.SaveVersion()

	tree.DeleteVersion(1)
	tree.DeleteVersion(2)

	tree.GetVersioned([]byte("Y"), 1)
	tree.GetVersioned([]byte("7"), 1)
	tree.GetVersioned([]byte("Z"), 1)
	tree.GetVersioned([]byte("6"), 1)
	tree.GetVersioned([]byte("s"), 1)
	tree.GetVersioned([]byte("2"), 1)
	tree.GetVersioned([]byte("4"), 1)
}

func TestVersionedCheckpointsSpecialCase7(t *testing.T) {
	tree, err := getTestTree(100)
	require.NoError(t, err)

	tree.Set([]byte("n"), []byte("OtqD3nyn"))
	tree.Set([]byte("W"), []byte("kMdhJjF5"))
	tree.Set([]byte("A"), []byte("BM3BnrIb"))
	tree.Set([]byte("I"), []byte("QvtCH970"))
	tree.Set([]byte("L"), []byte("txKgOTqD"))
	tree.Set([]byte("Y"), []byte("NAl7PC5L"))
	tree.SaveVersion()

	tree.Set([]byte("7"), []byte("qWcEAlyX"))
	tree.SaveVersion()

	tree.Set([]byte("M"), []byte("HdQwzA64"))
	tree.Set([]byte("3"), []byte("2Naa77fo"))
	tree.Set([]byte("A"), []byte("SRuwKOTm"))
	tree.Set([]byte("I"), []byte("oMX4aAOy"))
	tree.Set([]byte("4"), []byte("dKfvbEOc"))
	tree.SaveVersion()

	tree.Set([]byte("D"), []byte("3U4QbXCC"))
	tree.Set([]byte("B"), []byte("FxExhiDq"))
	tree.SaveVersion()

	tree.Set([]byte("A"), []byte("tWQgbFCY"))
	tree.SaveVersion()

	tree.DeleteVersion(4)

	tree.GetVersioned([]byte("A"), 3)
}

func TestVersionedTreeEfficiency(t *testing.T) {
	require := require.New(t)
	tree, err := NewMutableTree(db.NewMemDB(), 0)
	require.NoError(err)
	versions := 20
	keysPerVersion := 100
	keysAddedPerVersion := map[int]int{}

	keysAdded := 0
	for i := 1; i <= versions; i++ {
		for j := 0; j < keysPerVersion; j++ {
			// Keys of size one are likely to be overwritten.
			tree.Set([]byte(iavlrand.RandStr(1)), []byte(iavlrand.RandStr(8)))
		}
		sizeBefore := len(tree.ndb.nodes())
		tree.SaveVersion()
		sizeAfter := len(tree.ndb.nodes())
		change := sizeAfter - sizeBefore
		keysAddedPerVersion[i] = change
		keysAdded += change
	}

	keysDeleted := 0
	for i := 1; i < versions; i++ {
		if tree.VersionExists(int64(i)) {
			sizeBefore := len(tree.ndb.nodes())
			tree.DeleteVersion(int64(i))
			sizeAfter := len(tree.ndb.nodes())

			change := sizeBefore - sizeAfter
			keysDeleted += change

			require.InDelta(change, keysAddedPerVersion[i], float64(keysPerVersion)/5)
		}
	}
	require.Equal(keysAdded-tree.nodeSize(), keysDeleted)
}

func TestVersionedTreeProofs(t *testing.T) {
	require := require.New(t)
	tree, err := getTestTree(0)
	require.NoError(err)

	tree.Set([]byte("k1"), []byte("v1"))
	tree.Set([]byte("k2"), []byte("v1"))
	tree.Set([]byte("k3"), []byte("v1"))
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	// fmt.Println("TREE VERSION 1")
	// printNode(tree.ndb, tree.root, 0)
	// fmt.Println("TREE VERSION 1 END")

	root1 := tree.Hash()

	tree.Set([]byte("k2"), []byte("v2"))
	tree.Set([]byte("k4"), []byte("v2"))
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	// fmt.Println("TREE VERSION 2")
	// printNode(tree.ndb, tree.root, 0)
	// fmt.Println("TREE VERSION END")

	root2 := tree.Hash()
	require.NotEqual(root1, root2)

	tree.Remove([]byte("k2"))
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	// fmt.Println("TREE VERSION 3")
	// printNode(tree.ndb, tree.root, 0)
	// fmt.Println("TREE VERSION END")

	root3 := tree.Hash()
	require.NotEqual(root2, root3)

	val, proof, err := tree.GetVersionedWithProof([]byte("k2"), 1)
	require.NoError(err)
	require.EqualValues(val, []byte("v1"))
	require.NoError(proof.Verify(root1), proof.String())
	require.NoError(proof.VerifyItem([]byte("k2"), val))

	val, proof, err = tree.GetVersionedWithProof([]byte("k4"), 1)
	require.NoError(err)
	require.Nil(val)
	require.NoError(proof.Verify(root1))
	require.NoError(proof.VerifyAbsence([]byte("k4")))

	val, proof, err = tree.GetVersionedWithProof([]byte("k2"), 2)
	require.NoError(err)
	require.EqualValues(val, []byte("v2"))
	require.NoError(proof.Verify(root2), proof.String())
	require.NoError(proof.VerifyItem([]byte("k2"), val))

	val, proof, err = tree.GetVersionedWithProof([]byte("k1"), 2)
	require.NoError(err)
	require.EqualValues(val, []byte("v1"))
	require.NoError(proof.Verify(root2))
	require.NoError(proof.VerifyItem([]byte("k1"), val))

	val, proof, err = tree.GetVersionedWithProof([]byte("k2"), 3)

	require.NoError(err)
	require.Nil(val)
	require.NoError(proof.Verify(root3))
	require.NoError(proof.VerifyAbsence([]byte("k2")))
	require.Error(proof.Verify(root1))
	require.Error(proof.Verify(root2))
}

func TestOrphans(t *testing.T) {
	// If you create a sequence of saved versions
	// Then randomly delete versions other than the first and last until only those two remain
	// Any remaining orphan nodes should either have fromVersion == firstVersion || toVersion == lastVersion
	require := require.New(t)
	tree, err := NewMutableTree(db.NewMemDB(), 100)
	require.NoError(err)

	NUMVERSIONS := 100
	NUMUPDATES := 100

	for i := 0; i < NUMVERSIONS; i++ {
		for j := 1; j < NUMUPDATES; j++ {
			tree.Set(randBytes(2), randBytes(2))
		}
		_, _, err := tree.SaveVersion()
		require.NoError(err, "SaveVersion should not error")
	}

	idx := iavlrand.RandPerm(NUMVERSIONS - 2)
	for _, v := range idx {
		err := tree.DeleteVersion(int64(v + 1))
		require.NoError(err, "DeleteVersion should not error")
	}

	tree.ndb.traverseOrphans(func(k, v []byte) {
		var fromVersion, toVersion int64
		orphanKeyFormat.Scan(k, &toVersion, &fromVersion)
		require.True(fromVersion == int64(1) || toVersion == int64(99), fmt.Sprintf(`Unexpected orphan key exists: %v with fromVersion = %d and toVersion = %d.\n 
			Any orphan remaining in db should have either fromVersion == 1 or toVersion == 99. Since Version 1 and 99 are only versions in db`, k, fromVersion, toVersion))
	})
}

func TestVersionedTreeHash(t *testing.T) {
	require := require.New(t)
	tree, err := getTestTree(0)
	require.NoError(err)

	require.Equal("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hex.EncodeToString(tree.Hash()))
	tree.Set([]byte("I"), []byte("D"))
	require.Equal("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hex.EncodeToString(tree.Hash()))

	hash1, _, err := tree.SaveVersion()
	require.NoError(err)

	tree.Set([]byte("I"), []byte("F"))
	require.EqualValues(hash1, tree.Hash())

	hash2, _, err := tree.SaveVersion()
	require.NoError(err)

	val, proof, err := tree.GetVersionedWithProof([]byte("I"), 2)
	require.NoError(err)
	require.EqualValues([]byte("F"), val)
	require.NoError(proof.Verify(hash2))
	require.NoError(proof.VerifyItem([]byte("I"), val))
}

func TestNilValueSemantics(t *testing.T) {
	require := require.New(t)
	tree, err := getTestTree(0)
	require.NoError(err)

	require.Panics(func() {
		tree.Set([]byte("k"), nil)
	})
}

func TestCopyValueSemantics(t *testing.T) {
	require := require.New(t)

	tree, err := getTestTree(0)
	require.NoError(err)

	val := []byte("v1")

	tree.Set([]byte("k"), val)
	_, v := tree.Get([]byte("k"))
	require.Equal([]byte("v1"), v)

	val[1] = '2'

	_, val = tree.Get([]byte("k"))
	require.Equal([]byte("v2"), val)
}

func TestRollback(t *testing.T) {
	require := require.New(t)

	tree, err := getTestTree(0)
	require.NoError(err)

	tree.Set([]byte("k"), []byte("v"))
	tree.SaveVersion()

	tree.Set([]byte("r"), []byte("v"))
	tree.Set([]byte("s"), []byte("v"))

	tree.Rollback()

	tree.Set([]byte("t"), []byte("v"))

	tree.SaveVersion()

	require.Equal(int64(2), tree.Size())

	_, val := tree.Get([]byte("r"))
	require.Nil(val)

	_, val = tree.Get([]byte("s"))
	require.Nil(val)

	_, val = tree.Get([]byte("t"))
	require.Equal([]byte("v"), val)
}

func TestLazyLoadVersion(t *testing.T) {
	tree, err := getTestTree(0)
	require.NoError(t, err)
	maxVersions := 10

	version, err := tree.LazyLoadVersion(0)
	require.NoError(t, err, "unexpected error")
	require.Equal(t, version, int64(0), "expected latest version to be zero")

	for i := 0; i < maxVersions; i++ {
		tree.Set([]byte(fmt.Sprintf("key_%d", i+1)), []byte(fmt.Sprintf("value_%d", i+1)))

		_, _, err = tree.SaveVersion()
		require.NoError(t, err, "SaveVersion should not fail")
	}

	// require the ability to lazy load the latest version
	version, err = tree.LazyLoadVersion(int64(maxVersions))
	require.NoError(t, err, "unexpected error when lazy loading version")
	require.Equal(t, version, int64(maxVersions))

	_, value := tree.Get([]byte(fmt.Sprintf("key_%d", maxVersions)))
	require.Equal(t, value, []byte(fmt.Sprintf("value_%d", maxVersions)), "unexpected value")

	// require the ability to lazy load an older version
	version, err = tree.LazyLoadVersion(int64(maxVersions - 1))
	require.NoError(t, err, "unexpected error when lazy loading version")
	require.Equal(t, version, int64(maxVersions-1))

	_, value = tree.Get([]byte(fmt.Sprintf("key_%d", maxVersions-1)))
	require.Equal(t, value, []byte(fmt.Sprintf("value_%d", maxVersions-1)), "unexpected value")

	// require the inability to lazy load a non-valid version
	version, err = tree.LazyLoadVersion(int64(maxVersions + 1))
	require.Error(t, err, "expected error when lazy loading version")
	require.Equal(t, version, int64(maxVersions))
}

func TestOverwrite(t *testing.T) {
	require := require.New(t)

	mdb := db.NewMemDB()
	tree, err := NewMutableTree(mdb, 0)
	require.NoError(err)

	// Set one kv pair and save version 1
	tree.Set([]byte("key1"), []byte("value1"))
	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail")

	// Set another kv pair and save version 2
	tree.Set([]byte("key2"), []byte("value2"))
	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail")

	// Reload tree at version 1
	tree, err = NewMutableTree(mdb, 0)
	require.NoError(err)
	_, err = tree.LoadVersion(int64(1))
	require.NoError(err, "LoadVersion should not fail")

	// Attempt to put a different kv pair into the tree and save
	tree.Set([]byte("key2"), []byte("different value 2"))
	_, _, err = tree.SaveVersion()
	require.Error(err, "SaveVersion should fail because of changed value")

	// Replay the original transition from version 1 to version 2 and attempt to save
	tree.Set([]byte("key2"), []byte("value2"))
	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail, overwrite was idempotent")
}

func TestOverwriteEmpty(t *testing.T) {
	require := require.New(t)

	mdb := db.NewMemDB()
	tree, err := NewMutableTree(mdb, 0)
	require.NoError(err)

	// Save empty version 1
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	// Save empty version 2
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	// Save a key in version 3
	tree.Set([]byte("key"), []byte("value"))
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	// Load version 1 and attempt to save a different key
	_, err = tree.LoadVersion(1)
	require.NoError(err)
	tree.Set([]byte("foo"), []byte("bar"))
	_, _, err = tree.SaveVersion()
	require.Error(err)

	// However, deleting the key and saving an empty version should work,
	// since it's the same as the existing version.
	tree.Remove([]byte("foo"))
	_, version, err := tree.SaveVersion()
	require.NoError(err)
	require.EqualValues(2, version)
}

func TestLoadVersionForOverwriting(t *testing.T) {
	require := require.New(t)

	mdb := db.NewMemDB()
	tree, err := NewMutableTree(mdb, 0)
	require.NoError(err)

	maxLength := 100
	for count := 1; count <= maxLength; count++ {
		countStr := strconv.Itoa(count)
		// Set one kv pair and save version
		tree.Set([]byte("key"+countStr), []byte("value"+countStr))
		_, _, err = tree.SaveVersion()
		require.NoError(err, "SaveVersion should not fail")
	}

	tree, err = NewMutableTree(mdb, 0)
	require.NoError(err)
	targetVersion, _ := tree.LoadVersionForOverwriting(int64(maxLength * 2))
	require.Equal(targetVersion, int64(maxLength), "targetVersion shouldn't larger than the actual tree latest version")

	tree, err = NewMutableTree(mdb, 0)
	require.NoError(err)
	_, err = tree.LoadVersionForOverwriting(int64(maxLength / 2))
	require.NoError(err, "LoadVersion should not fail")

	for version := 1; version <= maxLength/2; version++ {
		exist := tree.VersionExists(int64(version))
		require.True(exist, "versions no more than 50 should exist")
	}

	for version := (maxLength / 2) + 1; version <= maxLength; version++ {
		exist := tree.VersionExists(int64(version))
		require.False(exist, "versions more than 50 should have been deleted")
	}

	tree.Set([]byte("key49"), []byte("value49 different"))
	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail, overwrite was allowed")

	tree.Set([]byte("key50"), []byte("value50 different"))
	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail, overwrite was allowed")

	// Reload tree at version 50, the latest tree version is 52
	tree, err = NewMutableTree(mdb, 0)
	require.NoError(err)
	_, err = tree.LoadVersion(int64(maxLength / 2))
	require.NoError(err, "LoadVersion should not fail")

	tree.Set([]byte("key49"), []byte("value49 different"))
	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail, write the same value")

	tree.Set([]byte("key50"), []byte("value50 different different"))
	_, _, err = tree.SaveVersion()
	require.Error(err, "SaveVersion should fail, overwrite was not allowed")

	tree.Set([]byte("key50"), []byte("value50 different"))
	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail, write the same value")

	// The tree version now is 52 which is equal to latest version.
	// Now any key value can be written into the tree
	tree.Set([]byte("key any value"), []byte("value any value"))
	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail.")
}

func TestDeleteVersionsCompare(t *testing.T) {
	require := require.New(t)

	var databaseSizeDeleteVersionsRange, databaseSizeDeleteVersion, databaseSizeDeleteVersions string

	const maxLength = 100
	const fromLength = 5
	{
		mdb := db.NewMemDB()
		tree, err := NewMutableTree(mdb, 0)
		require.NoError(err)

		versions := make([]int64, 0, maxLength)
		for count := 1; count <= maxLength; count++ {
			versions = append(versions, int64(count))
			countStr := strconv.Itoa(count)
			// Set kv pair and save version
			tree.Set([]byte("aaa"), []byte("bbb"))
			tree.Set([]byte("key"+countStr), []byte("value"+countStr))
			_, _, err = tree.SaveVersion()
			require.NoError(err, "SaveVersion should not fail")
		}

		tree, err = NewMutableTree(mdb, 0)
		require.NoError(err)
		targetVersion, err := tree.LoadVersion(int64(maxLength))
		require.NoError(err)
		require.Equal(targetVersion, int64(maxLength), "targetVersion shouldn't larger than the actual tree latest version")

		err = tree.DeleteVersionsRange(versions[fromLength], versions[int64(maxLength/2)])
		require.NoError(err, "DeleteVersionsRange should not fail")

		databaseSizeDeleteVersionsRange = mdb.Stats()["database.size"]
	}
	{
		mdb := db.NewMemDB()
		tree, err := NewMutableTree(mdb, 0)
		require.NoError(err)

		versions := make([]int64, 0, maxLength)
		for count := 1; count <= maxLength; count++ {
			versions = append(versions, int64(count))
			countStr := strconv.Itoa(count)
			// Set kv pair and save version
			tree.Set([]byte("aaa"), []byte("bbb"))
			tree.Set([]byte("key"+countStr), []byte("value"+countStr))
			_, _, err = tree.SaveVersion()
			require.NoError(err, "SaveVersion should not fail")
		}

		tree, err = NewMutableTree(mdb, 0)
		require.NoError(err)
		targetVersion, err := tree.LoadVersion(int64(maxLength))
		require.NoError(err)
		require.Equal(targetVersion, int64(maxLength), "targetVersion shouldn't larger than the actual tree latest version")

		for _, version := range versions[fromLength:int64(maxLength/2)] {
			err = tree.DeleteVersion(version)
			require.NoError(err, "DeleteVersion should not fail for %v", version)
		}

		databaseSizeDeleteVersion = mdb.Stats()["database.size"]
	}
	{
		mdb := db.NewMemDB()
		tree, err := NewMutableTree(mdb, 0)
		require.NoError(err)

		versions := make([]int64, 0, maxLength)
		for count := 1; count <= maxLength; count++ {
			versions = append(versions, int64(count))
			countStr := strconv.Itoa(count)
			// Set kv pair and save version
			tree.Set([]byte("aaa"), []byte("bbb"))
			tree.Set([]byte("key"+countStr), []byte("value"+countStr))
			_, _, err = tree.SaveVersion()
			require.NoError(err, "SaveVersion should not fail")
		}

		tree, err = NewMutableTree(mdb, 0)
		require.NoError(err)
		targetVersion, err := tree.LoadVersion(int64(maxLength))
		require.NoError(err)
		require.Equal(targetVersion, int64(maxLength), "targetVersion shouldn't larger than the actual tree latest version")

		err = tree.DeleteVersions(versions[fromLength:int64(maxLength/2)]...)
		require.NoError(err, "DeleteVersions should not fail")

		databaseSizeDeleteVersions = mdb.Stats()["database.size"]
	}

	require.Equal(databaseSizeDeleteVersion, databaseSizeDeleteVersionsRange)
	require.Equal(databaseSizeDeleteVersion, databaseSizeDeleteVersions)
}

// BENCHMARKS

func BenchmarkTreeLoadAndDelete(b *testing.B) {
	numVersions := 5000
	numKeysPerVersion := 10

	d, err := db.NewGoLevelDB("bench", ".")
	if err != nil {
		panic(err)
	}
	defer d.Close()
	defer os.RemoveAll("./bench.db")

	tree, err := NewMutableTree(d, 0)
	require.NoError(b, err)
	for v := 1; v < numVersions; v++ {
		for i := 0; i < numKeysPerVersion; i++ {
			tree.Set([]byte(iavlrand.RandStr(16)), iavlrand.RandBytes(32))
		}
		tree.SaveVersion()
	}

	b.Run("LoadAndDelete", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			b.StopTimer()
			tree, err = NewMutableTree(d, 0)
			require.NoError(b, err)
			runtime.GC()
			b.StartTimer()

			// Load the tree from disk.
			tree.Load()

			// Delete about 10% of the versions randomly.
			// The trade-off is usually between load efficiency and delete
			// efficiency, which is why we do both in this benchmark.
			// If we can load quickly into a data-structure that allows for
			// efficient deletes, we are golden.
			for v := 0; v < numVersions/10; v++ {
				version := (iavlrand.RandInt() % numVersions) + 1
				tree.DeleteVersion(int64(version))
			}
		}
	})
}

func TestLoadVersionForOverwritingCase2(t *testing.T) {
	require := require.New(t)

	tree, _ := NewMutableTreeWithOpts(db.NewMemDB(), 0, nil)

	for i := byte(0); i < 20; i++ {
		tree.Set([]byte{i}, []byte{i})
	}

	_, _, err := tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail")

	for i := byte(0); i < 20; i++ {
		tree.Set([]byte{i}, []byte{i + 1})
	}

	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail with the same key")

	for i := byte(0); i < 20; i++ {
		tree.Set([]byte{i}, []byte{i + 2})
	}
	tree.SaveVersion()

	removedNodes := []*Node{}

	for _, n := range tree.ndb.nodes() {
		if n.version > 1 {
			removedNodes = append(removedNodes, n)
		}
	}

	_, err = tree.LoadVersionForOverwriting(1)
	require.NoError(err, "LoadVersionForOverwriting should not fail")

	for i := byte(0); i < 20; i++ {
		_, v := tree.Get([]byte{i})
		require.Equal([]byte{i}, v)
	}

	for _, n := range removedNodes {
		has, _ := tree.ndb.Has(n.hash)
		require.False(has, "LoadVersionForOverwriting should remove useless nodes")
	}

	tree.Set([]byte{0x2}, []byte{0x3})

	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail")

	err = tree.DeleteVersion(1)
	require.NoError(err, "DeleteVersion should not fail")

	tree.Set([]byte{0x1}, []byte{0x3})

	_, _, err = tree.SaveVersion()
	require.NoError(err, "SaveVersion should not fail")
}

func TestLoadVersionForOverwritingCase3(t *testing.T) {
	require := require.New(t)

	tree, err := NewMutableTreeWithOpts(db.NewMemDB(), 0, nil)
	require.NoError(err)

	for i := byte(0); i < 20; i++ {
		tree.Set([]byte{i}, []byte{i})
	}
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	for i := byte(0); i < 20; i++ {
		tree.Set([]byte{i}, []byte{i + 1})
	}
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	removedNodes := []*Node{}

	for _, n := range tree.ndb.nodes() {
		if n.version > 1 {
			removedNodes = append(removedNodes, n)
		}
	}

	for i := byte(0); i < 20; i++ {
		tree.Remove([]byte{i})
	}
	_, _, err = tree.SaveVersion()
	require.NoError(err)

	_, err = tree.LoadVersionForOverwriting(1)
	require.NoError(err)
	for _, n := range removedNodes {
		has, err := tree.ndb.Has(n.hash)
		require.NoError(err)
		require.False(has, "LoadVersionForOverwriting should remove useless nodes")
	}

	for i := byte(0); i < 20; i++ {
		_, v := tree.Get([]byte{i})
		require.Equal([]byte{i}, v)
	}
}
