package ui

import "testing"

func TestLooksLikeHLS(t *testing.T) {
	for in, want := range map[string]bool{
		"https://s.pcloud.com/x/master.m3u8?token=1": true,
		"https://s.pcloud.com/x/MASTER.M3U8":         true,
		"https://s.pcloud.com/x/video.mp4":           false,
		"":                                           false,
	} {
		if got := looksLikeHLS(in); got != want {
			t.Errorf("looksLikeHLS(%q) = %v", in, got)
		}
	}
}
