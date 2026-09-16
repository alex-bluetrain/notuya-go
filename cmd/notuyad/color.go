package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var colorRGBAPattern = regexp.MustCompile(`(?i)^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)

// parseColor accepts #RRGGBB, RRGGBB, rgb(r,g,b) or rgba(r,g,b,a), matching
// cmd/notuya's parseColor.
func parseColor(raw string) (r, g, b uint8, err error) {
	raw = strings.TrimSpace(raw)
	if m := colorRGBAPattern.FindStringSubmatch(raw); m != nil {
		rv, _ := strconv.Atoi(m[1])
		gv, _ := strconv.Atoi(m[2])
		bv, _ := strconv.Atoi(m[3])
		return uint8(rv), uint8(gv), uint8(bv), nil
	}
	hexColor := strings.TrimPrefix(raw, "#")
	if len(hexColor) < 6 {
		return 0, 0, 0, fmt.Errorf("color: invalid format: %q", raw)
	}
	hexColor = hexColor[:6]
	rv, err1 := strconv.ParseUint(hexColor[0:2], 16, 8)
	gv, err2 := strconv.ParseUint(hexColor[2:4], 16, 8)
	bv, err3 := strconv.ParseUint(hexColor[4:6], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("color: invalid format: %q", raw)
	}
	return uint8(rv), uint8(gv), uint8(bv), nil
}
