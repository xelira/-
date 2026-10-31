//go:build !windows

package process

func KillStale(string) []int { return nil }
