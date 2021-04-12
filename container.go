package roar

import (
	"fmt"
	"math"
	"math/bits"
	"strings"
)

// container uses extra 8 bytes in the front as header.
// First 2 bytes are used for storing size of the container.
// The container size cannot exceed the vicinity of 8KB. At 8KB, we switch from packed arrays to
// bitmaps. We can fit the entire uint16 worth of bitmaps in 8KB (2^16 / 8 = 8 KB).

const (
	typeArray  uint16 = 0x00
	typeBitmap uint16 = 0x01

	indexSize        int = 0
	indexType        int = 1
	indexCardinality int = 2
	indexUnused      int = 3

	minSizeOfContainer = 8 + 2    // 2 bytes for allowing one uint16 to be added.
	maxSizeOfContainer = 8 + 8192 // 8192 for storing bitmap container.
	startIdx           = uint16(4)
)

// getSize returns the size of container in bytes. The way to calculate the uint16 data
// size is (byte size/2) - 4.
func getSize(data []byte) uint16 {
	x := toUint16Slice(data[:2])
	return x[0]
}
func setSize(data []byte, sz uint16) {
	x := toUint16Slice(data[:2])
	x[0] = sz
}
func dataAt(data []uint16, i int) uint16 {
	return data[int(startIdx)+i]
}

func getCardinality(data []uint16) int {
	return int(data[indexCardinality]) + int(data[indexCardinality+1])
}

func incrCardinality(data []uint16) {
	cur := getCardinality(data)
	if cur+1 > math.MaxUint16 {
		data[indexCardinality+1] = 1
	} else {
		data[indexCardinality]++
	}
}

func setCardinality(data []uint16, c int) {
	if c > math.MaxUint16 {
		data[indexCardinality+1] = 1
		data[indexCardinality] = math.MaxUint16
	} else {
		data[indexCardinality] = uint16(c)
	}
}

type array []uint16

// find returns the index of the first element >= x.
// The index is based on data portion of the container, ignoring startIdx.
// If the element > than all elements present, then N is returned where N = cardinality of the
// container.
func (c array) find(x uint16) int {
	// N := c[indexCardinality]
	N := getCardinality(c)
	for i := int(startIdx); i < int(startIdx)+N; i++ {
		if len(c) <= int(i) {
			panic(fmt.Sprintf("find: %d len(c) %d <= i %d\n", x, len(c), i))
		}
		if c[i] >= x {
			return int(i - int(startIdx))
		}
	}
	return N
}
func (c array) has(x uint16) bool {
	// N := c[indexCardinality]
	N := getCardinality(c)
	idx := c.find(x)
	if idx == N {
		return false
	}
	return c[int(startIdx)+idx] == x
}

func (c array) add(x uint16) bool {
	idx := c.find(x)
	// N := c[indexCardinality]
	N := getCardinality(c)
	offset := int(startIdx) + idx

	if int(idx) < N {
		if c[offset] == x {
			return false
		}
		// The entry at offset is the first entry, which is greater than x. Move it to the right.
		copy(c[offset+1:], c[offset:])
	}
	c[offset] = x
	incrCardinality(c)
	// c[indexCardinality] += 1
	return true
}

// TODO: Figure out how memory allocation would work in these situations. Perhaps use allocator here?
func (c array) andArray(other array) []uint16 {
	// min := min16(c[indexCardinality], other[indexCardinality])
	min := min(getCardinality(c), getCardinality(other))

	setc := c.all()
	seto := other.all()

	out := make([]uint16, int(startIdx)+min)
	num := uint16(intersection2by2(setc, seto, out[startIdx:]))

	// Truncate out to how many values were found.
	out = out[:startIdx+num+1]
	out[indexType] = typeArray
	out[indexSize] = uint16(sizeInBytesU16(len(out)))
	// out[indexCardinality] = uint16(num)
	setCardinality(out, int(num))
	return out
}

func (c array) orArray(other array) []uint16 {
	// max := c[indexCardinality] + other[indexCardinality]
	max := getCardinality(c) + getCardinality(other)
	if max > 4096 {
		// Use bitmap container.
		out := c.toBitmapContainer()
		data := out[startIdx:]

		// num := int(out[indexCardinality])
		num := getCardinality(out)
		for _, x := range other.all() {
			idx := x / 16
			pos := x % 16
			before := bits.OnesCount16(data[idx])
			data[idx] |= bitmapMask[pos]
			after := bits.OnesCount16(data[idx])
			num += after - before
		}
		// out[indexCardinality] = uint16(num)
		setCardinality(out, num)
		// For now, just keep it as a bitmap. No need to change if the
		// cardinality is smaller than 4096.
		return out
	}

	// The output would be of typeArray.
	out := make([]uint16, int(startIdx)+max)
	num := union2by2(c.all(), other.all(), out[startIdx:])
	out[indexType] = typeArray
	out[indexSize] = uint16(len(out) * 2)
	// out[indexCardinality] = uint16(num)
	setCardinality(out, num)
	return out
}

var tmp = make([]uint16, 8192)

func (c array) andBitmap(other bitmap) []uint16 {
	// out := make([]uint16, startIdx+c[indexCardinality]+2) // some extra space.
	out := make([]uint16, int(startIdx)+getCardinality(c)+2) // some extra space.
	out[indexType] = typeArray

	pos := startIdx
	for _, x := range c.all() {
		out[pos] = x
		pos += other.bitValue(x)
	}

	// Ensure we have at least one empty slot at the end.
	res := out[:pos+1]
	res[indexSize] = uint16(len(res) * 2)
	// res[indexCardinality] = pos - startIdx
	setCardinality(res, int(pos-startIdx))
	return res
}

func (c array) isFull() bool {
	// N := c[indexCardinality]
	N := getCardinality(c)
	return int(startIdx)+N >= len(c)
}

func (c array) all() []uint16 {
	// N := c[indexCardinality]
	N := getCardinality(c)
	return c[startIdx : int(startIdx)+N]
}

func (c array) minimum() uint16 {
	// N := c[indexCardinality]
	N := getCardinality(c)
	if N == 0 {
		return 0
	}
	return c[startIdx]
}

func (c array) maximum() uint16 {
	// N := c[indexCardinality]
	N := getCardinality(c)
	if N == 0 {
		return 0
	}
	return c[int(startIdx)+N-1]
}

func (c array) toBitmapContainer() []uint16 {
	buf := make([]byte, maxSizeOfContainer)
	b := bitmap(toUint16Slice(buf))
	b[indexSize] = maxSizeOfContainer
	b[indexType] = typeBitmap
	// b[indexCardinality] = c[indexCardinality]
	setCardinality(b, getCardinality(c))

	data := b[startIdx:]
	for _, x := range c.all() {
		idx := x / 16
		pos := x % 16
		data[idx] |= bitmapMask[pos]
		// num += bits.OnesCount16(data[idx])
	}
	// b[indexCardinality] = uint16(num)
	return b
}

func (c array) String() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Size: %d\n", c[0]))
	for i, val := range c[4:] {
		b.WriteString(fmt.Sprintf("%d: %d\n", i, val))
	}
	return b.String()
}

type bitmap []uint16

var bitmapMask []uint16

func init() {
	bitmapMask = make([]uint16, 16)
	for i := 0; i < 16; i++ {
		bitmapMask[i] = 1 << (15 - i)
	}
}

func (b bitmap) add(x uint16) bool {
	idx := x >> 4
	pos := x & 0xF

	if has := b[startIdx+idx] & bitmapMask[pos]; has > 0 {
		return false
	}

	b[startIdx+idx] |= bitmapMask[pos]
	// b[indexCardinality]++
	incrCardinality(b)
	return true
}

func (b bitmap) has(x uint16) bool {
	idx := x >> 4
	pos := x & 0xF

	has := b[startIdx+idx] & bitmapMask[pos]
	return has > 0
}

// TODO: This can perhaps be using SIMD instructions.
func (b bitmap) and(other bitmap) []uint16 {
	out := make([]uint16, maxSizeOfContainer)
	out[indexSize] = maxSizeOfContainer
	out[indexType] = typeBitmap
	var num int
	for i := 4; i < len(b); i++ {
		out[i] = b[i] & other[i]
		num += bits.OnesCount16(out[i])
	}
	setCardinality(out, num)
	// out[indexCardinality] = uint16(num)
	return out
}

func (b bitmap) orBitmap(other bitmap) []uint16 {
	out := make([]uint16, maxSizeOfContainer)
	copy(out, b) // Copy over first.

	var num int
	data := out[startIdx:]
	for i, v := range other[startIdx:] {
		data[i] |= v
		num += bits.OnesCount16(data[i])
	}
	// out[indexCardinality] = uint16(num)
	setCardinality(out, num)
	return out
}

func (b bitmap) orArray(other array) []uint16 {
	out := make([]uint16, maxSizeOfContainer)
	copy(out, b)

	// num := int(out[indexCardinality])
	num := getCardinality(out)
	for _, x := range other.all() {
		idx := x / 16
		pos := x % 16

		before := bits.OnesCount16(out[4+idx])
		out[4+idx] |= bitmapMask[pos]
		after := bits.OnesCount16(out[4+idx])
		num += after - before
	}
	// out[indexCardinality] = uint16(num)
	setCardinality(out, num)
	return out
}

func (b bitmap) ToArray() []uint16 {
	var res []uint16
	data := b[startIdx:]
	for idx := uint16(0); idx < uint16(len(data)); idx++ {
		x := data[idx]
		// TODO: This could potentially be optimized.
		for pos := uint16(0); pos < 16; pos++ {
			if x&bitmapMask[pos] > 0 {
				res = append(res, (idx<<4)|pos)
			}
		}
	}
	return res
}

// bitValue returns a 0 or a 1 depending upon whether x is present in the bitmap, where 1 means
// present and 0 means absent.
func (b bitmap) bitValue(x uint16) uint16 {
	idx := x >> 4
	return (b[4+idx] >> (15 - (x & 0xF))) & 1
}

func (b bitmap) isFull() bool {
	return false
}

func (b bitmap) minimum() uint16 {
	// N := b[indexCardinality]
	N := getCardinality(b)
	if N == 0 {
		return 0
	}
	for i, x := range b[startIdx:] {
		lz := bits.LeadingZeros16(x)
		if lz == 16 {
			continue
		}
		return uint16(16*i + lz)
	}
	panic("We shouldn't reach here")
}

func (b bitmap) maximum() uint16 {
	// N := b[indexCardinality]
	N := getCardinality(b)
	if N == 0 {
		return 0
	}
	for i := len(b); i >= int(startIdx); i-- {
		x := b[i]
		tz := bits.TrailingZeros16(x)
		if tz == 16 {
			continue
		}
		return uint16(16*i + 15 - tz)
	}
	panic("We shouldn't reach here")
}
