package launchpad

import (
	. "gopkg.in/check.v1"
)

var _ = Suite(&LPBugsSuite{})

type LPBugsSuite struct{}

var parseBugsTests = []struct {
	bugs []int
	line string
}{
	{[]int(nil), ""},
	{[]int(nil), "bug"},
	{[]int(nil), "bug #no"},
	{[]int{123}, "bug 123"},
	{[]int{123}, "bug #123"},
	{[]int{123}, "Bug #123"},
	{[]int{123}, "before bug #123 after"},
	{[]int{123}, "/+bug/123"},
	{[]int{456}, "123/+bug/456/789"},
	{[]int{456}, "123/bugs/456/789"},
	{[]int{123, 456}, "bug 123 bug 456"},
	{[]int{10000}, "#10000"},
	{[]int{100000}, "#100000"},
	{[]int{10000}, "before #10000 after"},
	{[]int{10000}, "(#10000)"},
	{[]int(nil), "RT#10000"},
	{[]int(nil), "1#10000"},
	{[]int(nil), "#1000"},
}

func (s *LPBugsSuite) TestParseBugs(c *C) {
	for _, test := range parseBugsTests {
		c.Assert(parseBugs(test.line), DeepEquals, test.bugs, Commentf("Line: %s", test.line))
	}
}
