package og

import (
	"io/fs"

	webfs "github.com/Alexander-D-Karpov/calendar/web"
)

const (
	regularFont  = "static/fonts/Inter-Regular.ttf"
	semiBoldFont = "static/fonts/Inter-SemiBold.ttf"
)

func readFont(name string) ([]byte, error) {
	return fs.ReadFile(webfs.FS, name)
}
