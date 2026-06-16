//go:build !windows

package cli

func isWindowsService() (bool, error) {
	return false, nil
}

func runServiceInSCM(stopChan chan struct{}, shutdownDoneChan chan struct{}) {
	// No-op em sistemas não-Windows
}

func ManageService(action string) {
	// No-op em sistemas não-Windows
}
