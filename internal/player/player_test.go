package player

import "testing"

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"rtsp://admin:secret@10.0.0.9:554/stream1": "rtsp://***@10.0.0.9:554/stream1",
		"rtsp://10.0.0.9/live?user=a@b":            "rtsp://10.0.0.9/live?user=a@b",
		"https://ha.lan/":                          "https://ha.lan/",
		"not a url":                                "not a url",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
