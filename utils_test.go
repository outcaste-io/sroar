package sroar

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemclr(t *testing.T) {
	b := [...]uint16{
		3: 10000 + 1,
		4: 10000 + 2,
		5: 10000 + 3,
		6: 10000 + 4,
	}
	t.Logf("%x", b)
	Memclr(b[:])
	for _, ui := range b {
		require.Equal(t, uint16(0), ui)
	}
}
