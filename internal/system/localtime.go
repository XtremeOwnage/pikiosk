package system

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	zoneMu   sync.Mutex
	zoneLink string
	zoneLoc  *time.Location
)

// Now returns the current time in the system's current timezone. Go reads
// the timezone once at startup, so a change made from the web UI would
// otherwise only apply after a restart.
func Now() time.Time {
	return time.Now().In(location())
}

func location() *time.Location {
	link, err := os.Readlink("/etc/localtime")
	if err != nil {
		return time.Local
	}
	zoneMu.Lock()
	defer zoneMu.Unlock()
	if link == zoneLink && zoneLoc != nil {
		return zoneLoc
	}
	name := link
	if i := strings.Index(link, "zoneinfo/"); i >= 0 {
		name = link[i+len("zoneinfo/"):]
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Not under a zoneinfo directory: read the file it points to.
		data, rerr := os.ReadFile(filepath.Clean(link))
		if rerr != nil {
			return time.Local
		}
		if loc, err = time.LoadLocationFromTZData(name, data); err != nil {
			return time.Local
		}
	}
	zoneLink, zoneLoc = link, loc
	return loc
}
