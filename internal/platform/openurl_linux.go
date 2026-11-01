//go:build linux && !android

package platform

import "os/exec"

func OpenURL(url string) error {
	return exec.Command("xdg-open", url).Start()
}
