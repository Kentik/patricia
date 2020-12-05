package int32_tree

import (
	"fmt"

	"github.com/kentik/patricia"
)

// TreeV4 is an IP Address patricia tree
type TreeV4 struct {
	nodes            []treeNodeV4 // root is always at [1] - [0] is unused
	availableIndexes []uint       // a place to store node indexes that we deleted, and are available
	tags             map[uint64]int32
}

// NewTreeV4 returns a new Tree
func NewTreeV4() *TreeV4 {
	return &TreeV4{
		nodes:            make([]treeNodeV4, 2, 2), // index 0 is skipped, 1 is root
		availableIndexes: make([]uint, 0),
		tags:             make(map[uint64]int32),
	}
}

// Clone creates an identical copy of the tree
// - Note: the items in the tree are not deep copied
func (t *TreeV4) Clone() *TreeV4 {
	ret := &TreeV4{
		nodes:            make([]treeNodeV4, len(t.nodes), cap(t.nodes)),
		availableIndexes: make([]uint, len(t.availableIndexes), cap(t.availableIndexes)),
		tags:             make(map[uint64]int32, len(t.tags)),
	}

	copy(ret.nodes, t.nodes)
	copy(ret.availableIndexes, t.availableIndexes)
	for k, v := range t.tags {
		ret.tags[k] = v
	}
	return ret
}

// CountTags iterates through the tree, counting the number of tags
// - note: unused nodes will have TagCount==0
func (t *TreeV4) CountTags() int {
	ret := 0
	for _, node := range t.nodes {
		ret += node.TagCount
	}
	return ret
}

// add a tag to the node at the input index, storing it in the first position if 'replaceFirst' is true
// - if matchFunc is non-nil, will enforce uniqueness at this node
// - returns whether the tag count was increased
func (t *TreeV4) addTag(tag int32, nodeIndex uint, matchFunc MatchesFunc, replaceFirst bool) bool {
	ret := true
	if replaceFirst {
		if t.nodes[nodeIndex].TagCount == 0 {
			t.nodes[nodeIndex].TagCount = 1
		} else {
			ret = false
		}
		t.tags[(uint64(nodeIndex) << 32)] = tag
	} else {
		key := (uint64(nodeIndex) << 32)
		tagCount := t.nodes[nodeIndex].TagCount
		if matchFunc != nil {
			// need to check if this value already exists
			for i := 0; i < tagCount; i++ {
				if matchFunc(t.tags[key+uint64(i)], tag) {
					return false
				}
			}
		}
		t.tags[key+(uint64(tagCount))] = tag
		t.nodes[nodeIndex].TagCount++

	}
	return ret
}
func (t *TreeV4) tagsForNode(nodeIndex uint) []int32 {
	if ret := t.tagsForNodeAppend(nil, nodeIndex); ret != nil {
		return ret
	} else {
		// NB: for compatibility with the old tagsForNode()
		// old comment: useful for base cases where we haven't found anything
		return make([]int32, 0)
	}
}

func (t *TreeV4) tagsForNodeAppend(ret []int32, nodeIndex uint) []int32 {
	if nodeIndex == 0 {
		return ret
	}

	// TODO: clean up the typing in here, between uint, uint64
	tagCount := t.nodes[nodeIndex].TagCount
	key := uint64(nodeIndex) << 32
	for i := 0; i < tagCount; i++ {
		ret = append(ret, t.tags[key+uint64(i)])
	}
	return ret
}

func (t *TreeV4) moveTags(fromIndex uint, toIndex uint) {
	tagCount := t.nodes[fromIndex].TagCount
	fromKey := uint64(fromIndex) << 32
	toKey := uint64(toIndex) << 32
	for i := 0; i < tagCount; i++ {
		t.tags[toKey+uint64(i)] = t.tags[fromKey+uint64(i)]
		delete(t.tags, fromKey+uint64(i))
	}
	t.nodes[toIndex].TagCount += t.nodes[fromIndex].TagCount
	t.nodes[fromIndex].TagCount = 0
}

func (t *TreeV4) firstTagForNode(nodeIndex uint) int32 {
	return t.tags[(uint64(nodeIndex) << 32)]
}

// delete tags at the input node, returning how many were deleted, and how many are left
func (t *TreeV4) deleteTag(nodeIndex uint, matchTag int32, matchFunc MatchesFunc) (int, int) {
	// TODO: this could be done much more efficiently

	// get tags
	tags := t.tagsForNode(nodeIndex)

	// delete tags
	for i := 0; i < t.nodes[nodeIndex].TagCount; i++ {
		delete(t.tags, (uint64(nodeIndex)<<32)+uint64(i))
	}
	t.nodes[nodeIndex].TagCount = 0

	// put them back
	deleteCount := 0
	keepCount := 0
	for _, tag := range tags {
		if matchFunc(tag, matchTag) {
			deleteCount++
		} else {
			// doesn't match - get to keep it
			t.addTag(tag, nodeIndex, matchFunc, false)
			keepCount++
		}
	}
	return deleteCount, keepCount
}

// Set the single value for a node - overwrites what's there
// Returns whether the tag count at this address was increased, and how many tags at this address
func (t *TreeV4) Set(address patricia.IPv4Address, tag int32) (bool, int, error) {
	return t.add(address, tag, nil, true)
}

// Add adds a tag to the tree
// - if matchFunc is non-nil, it will be used to ensure uniqueness at this node
// - returns whether the tag count at this address was increased, and how many tags at this address
func (t *TreeV4) Add(address patricia.IPv4Address, tag int32, matchFunc MatchesFunc) (bool, int, error) {
	return t.add(address, tag, matchFunc, false)
}

// add a tag to the tree, optionally as the single value
// - overwrites the first value in the list if 'replaceFirst' is true
// - returns whether the tag count was increased, and the number of tags at this address
func (t *TreeV4) add(address patricia.IPv4Address, tag int32, matchFunc MatchesFunc, replaceFirst bool) (bool, int, error) {
	// make sure we have more than enough capacity before we start adding to the tree, which invalidates pointers into the array
	if (len(t.availableIndexes) + cap(t.nodes)) < (len(t.nodes) + 10) {
		temp := make([]treeNodeV4, len(t.nodes), (cap(t.nodes)+1)*2)
		copy(temp, t.nodes)
		t.nodes = temp
	}

	root := &t.nodes[1]

	// handle root tags
	if address.Length == 0 {
		countIncreased := t.addTag(tag, 1, matchFunc, replaceFirst)
		return countIncreased, t.nodes[1].TagCount, nil
	}

	// root node doesn't have any prefix, so find the starting point
	nodeIndex := uint(0)
	parent := root
	if !address.IsLeftBitSet() {
		if root.Left == 0 {
			newNodeIndex := t.newNode(address, address.Length)
			countIncreased := t.addTag(tag, newNodeIndex, matchFunc, replaceFirst)
			root.Left = newNodeIndex
			return countIncreased, t.nodes[newNodeIndex].TagCount, nil
		}
		nodeIndex = root.Left
	} else {
		if root.Right == 0 {
			newNodeIndex := t.newNode(address, address.Length)
			countIncreased := t.addTag(tag, newNodeIndex, matchFunc, replaceFirst)
			root.Right = newNodeIndex
			return countIncreased, t.nodes[newNodeIndex].TagCount, nil
		}
		nodeIndex = root.Right
	}

	for {
		if nodeIndex == 0 {
			panic("Trying to traverse nodeIndex=0")
		}
		node := &t.nodes[nodeIndex]
		if node.prefixLength == 0 {
			panic("Reached a node with no prefix")
		}

		matchCount := uint(node.MatchCount(address))
		if matchCount == 0 {
			panic(fmt.Sprintf("Should not have traversed to a node with no prefix match - node prefix length: %d; address prefix length: %d", node.prefixLength, address.Length))
		}

		if matchCount == address.Length {
			// all the bits in the address matched

			if matchCount == node.prefixLength {
				// the whole prefix matched - we're done!
				countIncreased := t.addTag(tag, nodeIndex, matchFunc, replaceFirst)
				return countIncreased, t.nodes[nodeIndex].TagCount, nil
			}

			// the input address is shorter than the match found - need to create a new, intermediate parent
			newNodeIndex := t.newNode(address, address.Length)
			newNode := &t.nodes[newNodeIndex]
			countIncreased := t.addTag(tag, newNodeIndex, matchFunc, replaceFirst)

			// the existing node loses those matching bits, and becomes a child of the new node

			// shift
			node.ShiftPrefix(matchCount)

			if !node.IsLeftBitSet() {
				newNode.Left = nodeIndex
			} else {
				newNode.Right = nodeIndex
			}

			// now give this new node a home
			if parent.Left == nodeIndex {
				parent.Left = newNodeIndex
			} else {
				if parent.Right != nodeIndex {
					panic("node isn't left or right parent - should be impossible! (1)")
				}
				parent.Right = newNodeIndex
			}
			return countIncreased, t.nodes[newNodeIndex].TagCount, nil
		}

		if matchCount == node.prefixLength {
			// partial match - we have to keep traversing

			// chop off what's matched so far
			address.ShiftLeft(matchCount)

			if !address.IsLeftBitSet() {
				if node.Left == 0 {
					// nowhere else to go - create a new node here
					newNodeIndex := t.newNode(address, address.Length)
					countIncreased := t.addTag(tag, newNodeIndex, matchFunc, replaceFirst)
					node.Left = newNodeIndex
					return countIncreased, t.nodes[newNodeIndex].TagCount, nil
				}

				// there's a node to the left - traverse it
				parent = node
				nodeIndex = node.Left
				continue
			}

			// node didn't belong on the left, so it belongs on the right
			if node.Right == 0 {
				// nowhere else to go - create a new node here
				newNodeIndex := t.newNode(address, address.Length)
				countIncreased := t.addTag(tag, newNodeIndex, matchFunc, replaceFirst)
				node.Right = newNodeIndex
				return countIncreased, t.nodes[newNodeIndex].TagCount, nil
			}

			// there's a node to the right - traverse it
			parent = node
			nodeIndex = node.Right
			continue
		}

		// partial match with this node - need to split this node
		newCommonParentNodeIndex := t.newNode(address, matchCount)
		newCommonParentNode := &t.nodes[newCommonParentNodeIndex]

		// shift
		address.ShiftLeft(matchCount)

		newNodeIndex := t.newNode(address, address.Length)
		countIncreased := t.addTag(tag, newNodeIndex, matchFunc, replaceFirst)

		// see where the existing node fits - left or right
		node.ShiftPrefix(matchCount)
		if !node.IsLeftBitSet() {
			newCommonParentNode.Left = nodeIndex
			newCommonParentNode.Right = newNodeIndex
		} else {
			newCommonParentNode.Right = nodeIndex
			newCommonParentNode.Left = newNodeIndex
		}

		// now determine where the new node belongs
		if parent.Left == nodeIndex {
			parent.Left = newCommonParentNodeIndex
		} else {
			if parent.Right != nodeIndex {
				panic("node isn't left or right parent - should be impossible! (2)")
			}
			parent.Right = newCommonParentNodeIndex
		}
		return countIncreased, t.nodes[newNodeIndex].TagCount, nil
	}
}

// Delete a tag from the tree if it matches matchVal, as determined by matchFunc. Returns how many tags are removed
func (t *TreeV4) Delete(address patricia.IPv4Address, matchFunc MatchesFunc, matchVal int32) (int, error) {
	// traverse the tree, finding the node and its parent
	root := &t.nodes[1]
	var parentIndex uint
	var parent *treeNodeV4
	var targetNode *treeNodeV4
	var targetNodeIndex uint

	if address.Length == 0 {
		// caller just looking for root tags
		targetNode = root
		targetNodeIndex = 1
	} else {
		nodeIndex := uint(0)

		parentIndex = 1
		parent = root
		if !address.IsLeftBitSet() {
			nodeIndex = root.Left
		} else {
			nodeIndex = root.Right
		}

		// traverse the tree
		for {
			if nodeIndex == 0 {
				return 0, nil
			}

			node := &t.nodes[nodeIndex]
			matchCount := node.MatchCount(address)
			if matchCount < node.prefixLength {
				// didn't match the entire node - we're done
				return 0, nil
			}

			if matchCount == address.Length {
				// exact match - we're done
				targetNode = node
				targetNodeIndex = nodeIndex
				break
			}

			// there's still more address - keep traversing
			parentIndex = nodeIndex
			parent = node
			address.ShiftLeft(matchCount)
			if !address.IsLeftBitSet() {
				nodeIndex = node.Left
			} else {
				nodeIndex = node.Right
			}
		}
	}

	if targetNode == nil || targetNode.TagCount == 0 {
		// no tags found
		return 0, nil
	}

	// delete matching tags
	deleteCount, remainingTagCount := t.deleteTag(targetNodeIndex, matchVal, matchFunc)
	if remainingTagCount > 0 {
		// target node still has tags - we're not deleting it
		return deleteCount, nil
	}

	if targetNodeIndex == 1 {
		// can't delete the root node
		return deleteCount, nil
	}

	// compact the tree, if possible
	if targetNode.Left != 0 && targetNode.Right != 0 {
		// target has two children - nothing we can do - not deleting the node
		return deleteCount, nil
	} else if targetNode.Left != 0 {
		// target node only has only left child
		if parent.Left == targetNodeIndex {
			parent.Left = targetNode.Left
		} else {
			parent.Right = targetNode.Left
		}

		// need to update the child node prefix to include target node's
		tmpNode := &t.nodes[targetNode.Left]
		tmpNode.MergeFromNodes(targetNode, tmpNode)
	} else if targetNode.Right != 0 {
		// target node has only right child
		if parent.Left == targetNodeIndex {
			parent.Left = targetNode.Right
		} else {
			parent.Right = targetNode.Right
		}

		// need to update the child node prefix to include target node's
		tmpNode := &t.nodes[targetNode.Right]
		tmpNode.MergeFromNodes(targetNode, tmpNode)
	} else {
		// target node has no children - straight-up remove this node
		if parent.Left == targetNodeIndex {
			parent.Left = 0
			if parentIndex > 1 && parent.TagCount == 0 && parent.Right != 0 {
				// parent isn't root, has no tags, and there's a sibling - merge sibling into parent
				siblingIndexToDelete := parent.Right
				tmpNode := &t.nodes[siblingIndexToDelete]
				parent.MergeFromNodes(parent, tmpNode)

				// move tags
				t.moveTags(siblingIndexToDelete, parentIndex)

				// parent now gets target's sibling's children
				parent.Left = t.nodes[siblingIndexToDelete].Left
				parent.Right = t.nodes[siblingIndexToDelete].Right

				t.availableIndexes = append(t.availableIndexes, siblingIndexToDelete)
			}
		} else {
			parent.Right = 0
			if parentIndex > 1 && parent.TagCount == 0 && parent.Left != 0 {
				// parent isn't root, has no tags, and there's a sibling - merge sibling into parent
				siblingIndexToDelete := parent.Left
				tmpNode := &t.nodes[siblingIndexToDelete]
				parent.MergeFromNodes(parent, tmpNode)

				// move tags
				t.moveTags(siblingIndexToDelete, parentIndex)

				// parent now gets target's sibling's children
				parent.Right = t.nodes[parent.Left].Right
				parent.Left = t.nodes[parent.Left].Left

				t.availableIndexes = append(t.availableIndexes, siblingIndexToDelete)
			}
		}
	}

	targetNode.Left = 0
	targetNode.Right = 0
	t.availableIndexes = append(t.availableIndexes, targetNodeIndex)
	return deleteCount, nil
}

// FindTagsWithFilter finds all matching tags that passes the filter function
func (t *TreeV4) FindTagsWithFilter(address patricia.IPv4Address, filterFunc FilterFunc) ([]int32, error) {
	root := &t.nodes[1]
	if filterFunc == nil {
		return t.FindTags(address)
	}

	var matchCount uint
	ret := make([]int32, 0)

	if root.TagCount > 0 {
		for _, tag := range t.tagsForNode(1) {
			if filterFunc(tag) {
				ret = append(ret, tag)
			}
		}
	}

	if address.Length == 0 {
		// caller just looking for root tags
		return ret, nil
	}

	var nodeIndex uint
	if !address.IsLeftBitSet() {
		nodeIndex = root.Left
	} else {
		nodeIndex = root.Right
	}

	// traverse the tree
	count := 0
	for {
		count++
		if nodeIndex == 0 {
			return ret, nil
		}
		node := &t.nodes[nodeIndex]

		matchCount = node.MatchCount(address)
		if matchCount < node.prefixLength {
			// didn't match the entire node - we're done
			return ret, nil
		}

		// matched the full node - get its tags, then chop off the bits we've already matched and continue
		if node.TagCount > 0 {
			for _, tag := range t.tagsForNode(nodeIndex) {
				if filterFunc(tag) {
					ret = append(ret, tag)
				}
			}
		}

		if matchCount == address.Length {
			// exact match - we're done
			return ret, nil
		}

		// there's still more address - keep traversing
		address.ShiftLeft(matchCount)
		if !address.IsLeftBitSet() {
			nodeIndex = node.Left
		} else {
			nodeIndex = node.Right
		}
	}
}

// FindTags finds all matching tags for given address
func (t *TreeV4) FindTags(address patricia.IPv4Address) ([]int32, error) {
	if ret := t.FindTagsAppend(nil, address); ret != nil {
		// NB: the nil error is for compatibility with the old FindTags()
		return ret, nil
	} else {
		// NB: the alloc is for compatibility with the old FindTags()
		return make([]int32, 0), nil
	}
}

// FindTagsAppend finds all matching tags for given address and appends them to ret
func (t *TreeV4) FindTagsAppend(ret []int32, address patricia.IPv4Address) []int32 {
	var matchCount uint
	root := &t.nodes[1]

	if root.TagCount > 0 {
		ret = t.tagsForNodeAppend(ret, 1)
	}

	if address.Length == 0 {
		// caller just looking for root tags
		return ret
	}

	var nodeIndex uint
	if !address.IsLeftBitSet() {
		nodeIndex = root.Left
	} else {
		nodeIndex = root.Right
	}

	// traverse the tree
	count := 0
	for {
		count++
		if nodeIndex == 0 {
			return ret
		}
		node := &t.nodes[nodeIndex]

		matchCount = node.MatchCount(address)
		if matchCount < node.prefixLength {
			// didn't match the entire node - we're done
			return ret
		}

		// matched the full node - get its tags, then chop off the bits we've already matched and continue
		if node.TagCount > 0 {
			ret = t.tagsForNodeAppend(ret, nodeIndex)
		}

		if matchCount == address.Length {
			// exact match - we're done
			return ret
		}

		// there's still more address - keep traversing
		address.ShiftLeft(matchCount)
		if !address.IsLeftBitSet() {
			nodeIndex = node.Left
		} else {
			nodeIndex = node.Right
		}
	}
}

// FindDeepestTag finds a tag at the deepest level in the tree, representing the closest match.
// - if that target node has multiple tags, the first in the list is returned
func (t *TreeV4) FindDeepestTag(address patricia.IPv4Address) (bool, int32, error) {
	root := &t.nodes[1]
	var found bool
	var ret int32

	if root.TagCount > 0 {
		ret = t.firstTagForNode(1)
		found = true
	}

	if address.Length == 0 {
		// caller just looking for root tags
		return found, ret, nil
	}

	var nodeIndex uint
	if !address.IsLeftBitSet() {
		nodeIndex = root.Left
	} else {
		nodeIndex = root.Right
	}

	// traverse the tree
	for {
		if nodeIndex == 0 {
			return found, ret, nil
		}
		node := &t.nodes[nodeIndex]

		matchCount := node.MatchCount(address)
		if matchCount < node.prefixLength {
			// didn't match the entire node - we're done
			return found, ret, nil
		}

		// matched the full node - get its tags, then chop off the bits we've already matched and continue
		if node.TagCount > 0 {
			ret = t.firstTagForNode(nodeIndex)
			found = true
		}

		if matchCount == address.Length {
			// exact match - we're done
			return found, ret, nil
		}

		// there's still more address - keep traversing
		address.ShiftLeft(matchCount)
		if !address.IsLeftBitSet() {
			nodeIndex = node.Left
		} else {
			nodeIndex = node.Right
		}
	}
}

// FindDeepestTags finds all tags at the deepest level in the tree, representing the closest match
// - returns empty array if nothing found
func (t *TreeV4) FindDeepestTags(address patricia.IPv4Address) (bool, []int32, error) {
	root := &t.nodes[1]
	var found bool
	var retTagIndex uint

	if root.TagCount > 0 {
		retTagIndex = 1
		found = true
	}

	if address.Length == 0 {
		// caller just looking for root tags
		return found, t.tagsForNode(retTagIndex), nil
	}

	var nodeIndex uint
	if !address.IsLeftBitSet() {
		nodeIndex = root.Left
	} else {
		nodeIndex = root.Right
	}

	// traverse the tree
	for {
		if nodeIndex == 0 {
			return found, t.tagsForNode(retTagIndex), nil
		}
		node := &t.nodes[nodeIndex]

		matchCount := node.MatchCount(address)
		if matchCount < node.prefixLength {
			// didn't match the entire node - we're done
			return found, t.tagsForNode(retTagIndex), nil
		}

		// matched the full node - get its tags, then chop off the bits we've already matched and continue
		if node.TagCount > 0 {
			retTagIndex = nodeIndex
			found = true
		}

		if matchCount == address.Length {
			// exact match - we're done
			return found, t.tagsForNode(retTagIndex), nil
		}

		// there's still more address - keep traversing
		address.ShiftLeft(matchCount)
		if !address.IsLeftBitSet() {
			nodeIndex = node.Left
		} else {
			nodeIndex = node.Right
		}
	}
}

// note: this is only used for unit testing
func (t *TreeV4) countNodes(nodeIndex uint) int {
	nodeCount := 1

	node := &t.nodes[nodeIndex]
	if node.Left != 0 {
		nodeCount += t.countNodes(node.Left)
	}
	if node.Right != 0 {
		nodeCount += t.countNodes(node.Right)
	}
	return nodeCount
}

// note: this is only used for unit testing
func (t *TreeV4) countTags(nodeIndex uint) int {
	node := &t.nodes[nodeIndex]

	tagCount := node.TagCount
	if node.Left != 0 {
		tagCount += t.countTags(node.Left)
	}
	if node.Right != 0 {
		tagCount += t.countTags(node.Right)
	}
	return tagCount
}
