// Package ftpserver provides all the tools to build your own FTP server: The core library and the driver.
package ftpserver

import (
	"regexp"
	"testing"
)

func testRegexMatch(t *testing.T, regexp *regexp.Regexp, strings []string, expectedMatch bool) {
	for _, s := range strings {
		if regexp.Match([]byte(s)) != expectedMatch {
			t.Errorf("Invalid match result: %s", s)
		}
	}
}

func TestRemoteAddrFormat(t *testing.T) {
	testRegexMatch(t, remoteAddrRegex, []string{"1,2,3,4,5,6"}, true)
	testRegexMatch(t, remoteAddrRegex, []string{"1,2,3,4,5"}, false)
}
