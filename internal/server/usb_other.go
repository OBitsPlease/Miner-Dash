//go:build !windows

package server

import (
	"errors"
	"os"
)

func platformUSBSupported() bool {
	return false
}

func platformUSBDevices() ([]usbDevice, error) {
	return []usbDevice{}, nil
}

func platformPrepareUSB(usbDevice) error {
	return errors.New("direct USB writing is available only on a Windows controller")
}

func platformOpenUSB(usbDevice, bool) (*os.File, error) {
	return nil, errors.New("direct USB writing is available only on a Windows controller")
}

func platformRefreshUSB(usbDevice) error {
	return errors.New("direct USB writing is available only on a Windows controller")
}

func platformRestoreUSB(usbDevice) error {
	return errors.New("USB restoration is available only on a Windows controller")
}

func platformWriteUSBProvisioning(usbDevice, []byte) error {
	return errors.New("USB pairing is available only on a Windows controller")
}
