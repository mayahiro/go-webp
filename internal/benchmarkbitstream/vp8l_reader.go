package benchmarkbitstream

import "fmt"

const (
	vp8lCodeLengthAlphabetSize = 19
	vp8lMaximumCodeLength      = 15
)

var vp8lCodeLengthOrder = [...]uint8{
	17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
}

type vp8lBitReader struct {
	data  []byte
	off   int
	bits  uint64
	nBits uint8
}

func (r *vp8lBitReader) read(n uint8) (uint32, error) {
	if n > 32 {
		return 0, fmt.Errorf("VP8L read width %d exceeds 32 bits", n)
	}
	for r.nBits < n {
		if r.off >= len(r.data) {
			return 0, fmt.Errorf("unexpected end of VP8L data at bit %d", r.position())
		}
		r.bits |= uint64(r.data[r.off]) << r.nBits
		r.nBits += 8
		r.off++
	}
	if n == 0 {
		return 0, nil
	}
	value := uint32(r.bits & (uint64(1)<<n - 1))
	r.bits >>= n
	r.nBits -= n
	return value, nil
}

func (r *vp8lBitReader) position() uint64 {
	return uint64(r.off*8) - uint64(r.nBits)
}

type vp8lTreeNode struct {
	child  [2]int32
	symbol int32
}

type vp8lTree struct {
	constant bool
	symbol   int
	nodes    []vp8lTreeNode
}

func readVP8LTree(r *vp8lBitReader, alphabetSize int) (vp8lTree, error) {
	if alphabetSize <= 0 {
		return vp8lTree{}, fmt.Errorf("invalid VP8L alphabet size %d", alphabetSize)
	}
	useSimple, err := r.read(1)
	if err != nil {
		return vp8lTree{}, err
	}
	if useSimple != 0 {
		return readSimpleVP8LTree(r, alphabetSize)
	}
	return readNormalVP8LTree(r, alphabetSize)
}

func readSimpleVP8LTree(r *vp8lBitReader, alphabetSize int) (vp8lTree, error) {
	nSymbolsMinusOne, err := r.read(1)
	if err != nil {
		return vp8lTree{}, err
	}
	use8Bits, err := r.read(1)
	if err != nil {
		return vp8lTree{}, err
	}
	firstBits := uint8(1)
	if use8Bits != 0 {
		firstBits = 8
	}
	first, err := r.read(firstBits)
	if err != nil {
		return vp8lTree{}, err
	}
	if int(first) >= alphabetSize {
		return vp8lTree{}, fmt.Errorf("VP8L simple tree symbol %d exceeds alphabet %d", first, alphabetSize)
	}
	if nSymbolsMinusOne == 0 {
		return vp8lTree{constant: true, symbol: int(first)}, nil
	}
	second, err := r.read(8)
	if err != nil {
		return vp8lTree{}, err
	}
	if int(second) >= alphabetSize {
		return vp8lTree{}, fmt.Errorf("VP8L simple tree symbol %d exceeds alphabet %d", second, alphabetSize)
	}
	lengths := make([]uint8, alphabetSize)
	lengths[first] = 1
	lengths[second] = 1
	return buildVP8LTree(lengths)
}

func readNormalVP8LTree(r *vp8lBitReader, alphabetSize int) (vp8lTree, error) {
	nCodesMinusFour, err := r.read(4)
	if err != nil {
		return vp8lTree{}, err
	}
	nCodes := int(nCodesMinusFour) + 4
	if nCodes > len(vp8lCodeLengthOrder) {
		return vp8lTree{}, fmt.Errorf("VP8L code length count %d exceeds %d", nCodes, len(vp8lCodeLengthOrder))
	}
	codeLengthLengths := make([]uint8, vp8lCodeLengthAlphabetSize)
	for i := 0; i < nCodes; i++ {
		length, err := r.read(3)
		if err != nil {
			return vp8lTree{}, err
		}
		codeLengthLengths[vp8lCodeLengthOrder[i]] = uint8(length)
	}
	codeLengthTree, err := buildVP8LTree(codeLengthLengths)
	if err != nil {
		return vp8lTree{}, fmt.Errorf("VP8L code length tree: %w", err)
	}
	useMaximumSymbol, err := r.read(1)
	if err != nil {
		return vp8lTree{}, err
	}
	maximumSymbol := alphabetSize
	if useMaximumSymbol != 0 {
		selector, err := r.read(3)
		if err != nil {
			return vp8lTree{}, err
		}
		nBits := uint8(2 + 2*selector)
		value, err := r.read(nBits)
		if err != nil {
			return vp8lTree{}, err
		}
		maximumSymbol = int(value) + 2
		if maximumSymbol > alphabetSize {
			return vp8lTree{}, fmt.Errorf("VP8L maximum symbol %d exceeds alphabet %d", maximumSymbol, alphabetSize)
		}
	}

	lengths := make([]uint8, alphabetSize)
	previousNonZero := uint8(8)
	for symbol, remainingTokens := 0, maximumSymbol; symbol < alphabetSize && remainingTokens > 0; remainingTokens-- {
		codeLengthSymbol, err := decodeVP8LTreeSymbol(r, &codeLengthTree)
		if err != nil {
			return vp8lTree{}, fmt.Errorf("VP8L code length at symbol %d: %w", symbol, err)
		}
		switch {
		case codeLengthSymbol <= 15:
			lengths[symbol] = uint8(codeLengthSymbol)
			if codeLengthSymbol != 0 {
				previousNonZero = uint8(codeLengthSymbol)
			}
			symbol++
		case codeLengthSymbol == 16:
			extra, err := r.read(2)
			if err != nil {
				return vp8lTree{}, err
			}
			repeat := int(extra) + 3
			if symbol+repeat > alphabetSize {
				return vp8lTree{}, fmt.Errorf("VP8L repeated code length exceeds alphabet %d", alphabetSize)
			}
			for range repeat {
				lengths[symbol] = previousNonZero
				symbol++
			}
		case codeLengthSymbol == 17:
			extra, err := r.read(3)
			if err != nil {
				return vp8lTree{}, err
			}
			symbol += int(extra) + 3
			if symbol > alphabetSize {
				return vp8lTree{}, fmt.Errorf("VP8L zero repeat exceeds alphabet %d", alphabetSize)
			}
		case codeLengthSymbol == 18:
			extra, err := r.read(7)
			if err != nil {
				return vp8lTree{}, err
			}
			symbol += int(extra) + 11
			if symbol > alphabetSize {
				return vp8lTree{}, fmt.Errorf("VP8L long zero repeat exceeds alphabet %d", alphabetSize)
			}
		default:
			return vp8lTree{}, fmt.Errorf("invalid VP8L code length symbol %d", codeLengthSymbol)
		}
	}
	return buildVP8LTree(lengths)
}

func buildVP8LTree(lengths []uint8) (vp8lTree, error) {
	nonZero := 0
	constantSymbol := 0
	for symbol, length := range lengths {
		if length > vp8lMaximumCodeLength {
			return vp8lTree{}, fmt.Errorf("VP8L code length %d exceeds %d", length, vp8lMaximumCodeLength)
		}
		if length != 0 {
			nonZero++
			constantSymbol = symbol
		}
	}
	if nonZero == 0 {
		return vp8lTree{}, nil
	}
	if nonZero == 1 {
		return vp8lTree{constant: true, symbol: constantSymbol}, nil
	}

	codes := canonicalVP8LCodes(lengths)
	root := newVP8LTreeNode()
	tree := vp8lTree{nodes: []vp8lTreeNode{root}}
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		code := reverseVP8LBits(codes[symbol], length)
		nodeIndex := int32(0)
		for bitIndex := uint8(0); bitIndex < length; bitIndex++ {
			bit := (code >> bitIndex) & 1
			next := tree.nodes[nodeIndex].child[bit]
			if next < 0 {
				next = int32(len(tree.nodes))
				tree.nodes[nodeIndex].child[bit] = next
				tree.nodes = append(tree.nodes, newVP8LTreeNode())
			}
			nodeIndex = next
		}
		if tree.nodes[nodeIndex].symbol >= 0 {
			return vp8lTree{}, fmt.Errorf("duplicate VP8L Huffman code for symbol %d", symbol)
		}
		tree.nodes[nodeIndex].symbol = int32(symbol)
	}
	return tree, nil
}

func newVP8LTreeNode() vp8lTreeNode {
	return vp8lTreeNode{child: [2]int32{-1, -1}, symbol: -1}
}

func decodeVP8LTreeSymbol(r *vp8lBitReader, tree *vp8lTree) (int, error) {
	if tree.constant {
		return tree.symbol, nil
	}
	if len(tree.nodes) == 0 {
		return 0, fmt.Errorf("attempted to decode an empty VP8L tree")
	}
	nodeIndex := int32(0)
	for depth := 0; depth <= vp8lMaximumCodeLength; depth++ {
		node := tree.nodes[nodeIndex]
		if node.symbol >= 0 {
			return int(node.symbol), nil
		}
		bit, err := r.read(1)
		if err != nil {
			return 0, err
		}
		nodeIndex = node.child[bit]
		if nodeIndex < 0 || int(nodeIndex) >= len(tree.nodes) {
			return 0, fmt.Errorf("invalid VP8L Huffman code")
		}
	}
	return 0, fmt.Errorf("VP8L Huffman code exceeds %d bits", vp8lMaximumCodeLength)
}

func canonicalVP8LCodes(lengths []uint8) []uint16 {
	var histogram [vp8lMaximumCodeLength + 1]uint16
	for _, length := range lengths {
		if length != 0 {
			histogram[length]++
		}
	}
	var nextCodes [vp8lMaximumCodeLength + 1]uint16
	code := uint16(0)
	for length := 1; length < len(nextCodes); length++ {
		code = (code + histogram[length-1]) << 1
		nextCodes[length] = code
	}
	codes := make([]uint16, len(lengths))
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

func reverseVP8LBits(value uint16, n uint8) uint16 {
	var reversed uint16
	for range n {
		reversed = reversed<<1 | value&1
		value >>= 1
	}
	return reversed
}
