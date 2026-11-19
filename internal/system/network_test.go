package system

import (
	"reflect"
	"testing"
)

func TestSplitTerse(t *testing.T) {
	cases := map[string][]string{
		`eth0:ethernet:connected:Wired connection 1`: {"eth0", "ethernet", "connected", "Wired connection 1"},
		`yes:My\:SSID:72:WPA2`:                       {"yes", "My:SSID", "72", "WPA2"},
		`a\\b:c`:                                     {`a\b`, "c"},
		`IP4.ADDRESS[1]:10.0.0.5/24`:                 {"IP4.ADDRESS[1]", "10.0.0.5/24"},
		`::`:                                         {"", "", ""},
	}
	for in, want := range cases {
		if got := splitTerse(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
