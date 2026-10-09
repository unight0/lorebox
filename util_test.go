package main

import (
	"testing"
	"fmt"
)

func TestHasPostfix(t *testing.T) {
	testCases := []struct {
		str, postfix string
		expect bool
	} {
		{"has/postfix", "/postfix", true},
		{"1", "1234", false},
		{"no/postfixx", "/postfix", false},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Test(%s,%s):%t", tc.str, tc.postfix, tc.expect), func(t *testing.T) {
			actual := hasPostfix(tc.str, tc.postfix)
			if (actual != tc.expect) {
				t.Errorf("hasPostfix(%s, %s) should return %t", tc.str, tc.postfix, tc.expect)
			}
		})
	}
}

func TestChopPostfix(t *testing.T) {
	testCases := []struct {
		str, postfix, expect string	
	} {
		{"abcaaa", "a", "abcaa"},
		{"xxxyz", "yz", "xxx"},
		{"......", ".", "....."},
		{"aaaaaa", "b", "aaaaaa"},
		{"//////", "X", "//////"},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Test(%s,%s):'%s'",  tc.str, tc.postfix, tc.expect), func(t *testing.T) {
			actual := chopPostfix(tc.str, tc.postfix)
			if (actual != tc.expect) {
				t.Errorf("chopPostfix(%s) should return '%s', got '%s'", tc.str, tc.expect, actual)
			}
		})
	}
}

func TestChopInfoRefs(t *testing.T) {
	testCases := []struct {
		path string	
		expect string
	} {
		{"a/b/info/refs", "a/b"},
		{"/~/user1/repo1/info/refs", "/~/user1/repo1"},
		{"info/refs", "info/refs"},
		{"/////~/userX", "/////~/userX"},
		{"xxx", "xxx"},
		{"/~userX", "/~userX"},
		{"~userX", "~userX"},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Test(%s):'%s'",  tc.path, tc.expect), func(t *testing.T) {
			actual := chopInfoRefs(tc.path)
			if (actual != tc.expect) {
				t.Errorf("chopInfoRefs(%s) should return '%s', got '%s'", tc.path, tc.expect, actual)
			}
		})
	}
}

func TestIsSelfHosted(t *testing.T) {
	testCases := []struct {
		repo RepoPath
		expect bool
	} {
		{NewRepoPath("~/user1/repo1"), true},
		{NewRepoPath("/~/user1/repo1/"), true},
		{NewRepoPath("/////~/userX"), true},
		{NewRepoPath("~/userX/"), true},
		{NewRepoPath("/~user1/repo1/"), false},
		{NewRepoPath("/~userX"), false},
		{NewRepoPath("~userX"), false},
		{NewRepoPath("/xxx/~/userX"), false},
		{NewRepoPath("/asdf/asdf/~/userX"), false},
		{NewRepoPath("/a/b/c/~/userX"), false},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Test(%t):%s", tc.expect, tc.repo), func(t *testing.T) {
			if (selfHosted(tc.repo) != tc.expect) {
				t.Errorf("selfHosted(%s) should return %t", tc.repo, tc.expect)
			}
		})
	}
}

func TestParseSelfHosted(t *testing.T) {
	testCases := []struct {
		repo RepoPath
		owner, name string	
	} {
		{NewRepoPath("~/user1/repo1"), "user1", "repo1"},
		{NewRepoPath("/~/user1/repo1/"), "user1", "repo1"},
		{NewRepoPath("~/user1/"), "", ""},
		{NewRepoPath("/////~/userX"), "", ""},
		{NewRepoPath("/~user1/repo1/"), "", ""},
		{NewRepoPath("/~userX"), "", ""},
		{NewRepoPath("~userX"), "", ""},
		{NewRepoPath("/xxx/~/userX"), "", ""},
		{NewRepoPath("/asdf/asdf/~/userX"), "", ""},
		{NewRepoPath("/a/b/c/~/userX"), "", ""},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Test(%s):'%s','%s'", tc.repo, tc.owner, tc.name), func(t *testing.T) {
			owner, name := parseSelfHosted(tc.repo)
			if (owner != tc.owner || name != tc.name) {
				t.Errorf("parseSelfHosted(%s) should return %s, %s", tc.repo, tc.owner, tc.name)
			}
		})
	}
}

func TestParseDiskSize(t *testing.T) {
	testCases := []struct {
		size string
		res int64
		err bool
	} {
		{"1024", 1024, false},
		{"1024K", 1024 * 1024, false},
		{"1024M", 1024 * 1024 * 1024, false},
		{"1024G", 1024 * 1024 * 1024 * 1024, false},
		{"1024X", 0, true},
		{"X1024X", 0, true},
		{"X1024XG", 0, true},
		{"X1024G", 0, true},
		{"G", 0, true},
		{"M", 0, true},
		{"K", 0, true},
		{"", 0, true},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Test(%s):%d,%t", tc.size, tc.res, tc.err), func(t *testing.T) {
			res, err := parseDiskSize(tc.size)
			haveErr := err != nil
			if (res != tc.res || haveErr != tc.err) {
				t.Errorf("parseDiskSize(%s) should return %d, haveErr=%t", tc.size, tc.res, tc.err)
			}
		})
	}
}
