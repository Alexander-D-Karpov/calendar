package view

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

func GridCSS() []byte {
	var b bytes.Buffer
	for i := 1; i <= SlotsPerDay; i++ {
		fmt.Fprintf(&b, ".gr-%d{grid-row-start:%d}\n", i, i)
	}
	for i := 1; i <= SlotsPerDay; i++ {
		fmt.Fprintf(&b, ".gs-%d{grid-row-end:span %d}\n", i, i)
	}
	for n := 1; n <= MaxLanes; n++ {
		for i := range n {
			fmt.Fprintf(&b, ".lane-%d-%d{margin-left:%s%%;width:calc(%s%% - 2px)}\n", i, n, pct(i, n), pct(1, n))
		}
	}
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&b, ".cols-%d{--n:%d}\n.gcol-%d{grid-column-start:%d}\n.gspan-%d{grid-column-end:span %d}\n", i, i, i, i, i, i)
	}
	for i := 1; i <= MaxRows; i++ {
		fmt.Fprintf(&b, ".grow-%d{grid-row-start:%d}\n", i, i)
	}
	return b.Bytes()
}

func pct(a, n int) string {
	s := strconv.FormatFloat(float64(a)*100/float64(n), 'f', 4, 64)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}
