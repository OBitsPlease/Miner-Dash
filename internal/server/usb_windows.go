//go:build windows

package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func platformUSBSupported() bool {
	return true
}

func platformUSBDevices() ([]usbDevice, error) {
	output, err := runUSBPowerShell(`
$devices = @(
  Get-Disk | Where-Object {
    $_.BusType -eq 'USB' -and -not $_.IsBoot -and -not $_.IsSystem
  } | ForEach-Object {
    [PSCustomObject]@{
      DiskNumber = [int]$_.Number
      Name = [string]$_.FriendlyName
      SerialNumber = [string]$_.SerialNumber
      UniqueID = [string]$_.UniqueId
      SizeBytes = [long]$_.Size
      PartitionStyle = [string]$_.PartitionStyle
    }
  }
)
ConvertTo-Json -Compress -InputObject $devices
`)
	if err != nil {
		return nil, fmt.Errorf("list removable USB drives: %w", err)
	}
	var records []struct {
		DiskNumber     int
		Name           string
		SerialNumber   string
		UniqueID       string
		SizeBytes      int64
		PartitionStyle string
	}
	if strings.TrimSpace(string(output)) == "" {
		return []usbDevice{}, nil
	}
	if err := json.Unmarshal(output, &records); err != nil {
		return nil, fmt.Errorf("decode removable USB drive list: %w", err)
	}
	devices := make([]usbDevice, 0, len(records))
	for _, record := range records {
		if record.DiskNumber < 0 || record.SizeBytes <= 0 {
			continue
		}
		device := usbDevice{
			DiskNumber:     record.DiskNumber,
			Name:           strings.TrimSpace(record.Name),
			SerialNumber:   strings.TrimSpace(record.SerialNumber),
			SizeBytes:      record.SizeBytes,
			PartitionStyle: strings.TrimSpace(record.PartitionStyle),
			uniqueID:       strings.TrimSpace(record.UniqueID),
		}
		device.ID = fingerprintUSBDevice(device)
		devices = append(devices, device)
	}
	return devices, nil
}

func platformPrepareUSB(device usbDevice) error {
	script := fmt.Sprintf(`
$disk = Get-Disk -Number %d -ErrorAction Stop
if ($disk.BusType -ne 'USB' -or $disk.IsBoot -or $disk.IsSystem -or [long]$disk.Size -ne %d) {
  throw 'Selected disk failed the final removable USB safety check.'
}
if ($disk.IsOffline) { Set-Disk -Number %d -IsOffline $false -ErrorAction Stop }
if ($disk.IsReadOnly) { Set-Disk -Number %d -IsReadOnly $false -ErrorAction Stop }
Clear-Disk -Number %d -RemoveData -RemoveOEM -Confirm:$false -ErrorAction Stop
`, device.DiskNumber, device.SizeBytes, device.DiskNumber, device.DiskNumber, device.DiskNumber)
	if output, err := runUSBPowerShell(script); err != nil {
		return fmt.Errorf("prepare USB drive: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func platformOpenUSB(device usbDevice, write bool) (*os.File, error) {
	path := fmt.Sprintf(`\\.\PhysicalDrive%d`, device.DiskNumber)
	flag := os.O_RDONLY
	if write {
		flag = os.O_WRONLY
	}
	file, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return file, nil
}

func platformRefreshUSB(device usbDevice) error {
	output, err := runUSBPowerShell(fmt.Sprintf(`Update-Disk -Number %d -ErrorAction Stop`, device.DiskNumber))
	if err != nil {
		return fmt.Errorf("refresh USB drive: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func platformRestoreUSB(device usbDevice) error {
	script := fmt.Sprintf(`
$disk = Get-Disk -Number %d -ErrorAction Stop
if ($disk.BusType -ne 'USB' -or $disk.IsBoot -or $disk.IsSystem -or [long]$disk.Size -ne %d) {
  throw 'Selected disk failed the final removable USB safety check.'
}
if ($disk.IsOffline) { Set-Disk -Number %d -IsOffline $false -ErrorAction Stop }
if ($disk.IsReadOnly) { Set-Disk -Number %d -IsReadOnly $false -ErrorAction Stop }
Clear-Disk -Number %d -RemoveData -RemoveOEM -Confirm:$false -ErrorAction Stop
Initialize-Disk -Number %d -PartitionStyle GPT -ErrorAction Stop | Out-Null
$partition = New-Partition -DiskNumber %d -UseMaximumSize -AssignDriveLetter -ErrorAction Stop
Format-Volume -Partition $partition -FileSystem exFAT -NewFileSystemLabel 'MINERDASH_USB' -Confirm:$false -Force -ErrorAction Stop | Out-Null
`, device.DiskNumber, device.SizeBytes, device.DiskNumber, device.DiskNumber, device.DiskNumber, device.DiskNumber, device.DiskNumber)
	if output, err := runUSBPowerShell(script); err != nil {
		return fmt.Errorf("restore USB drive: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func platformWriteUSBProvisioning(device usbDevice, provisioning []byte) error {
	encoded := base64.StdEncoding.EncodeToString(provisioning)
	script := fmt.Sprintf(`
$disk = Get-Disk -Number %d -ErrorAction Stop
if ($disk.BusType -ne 'USB' -or $disk.IsBoot -or $disk.IsSystem -or [long]$disk.Size -ne %d) {
  throw 'Selected disk failed the final removable USB safety check.'
}
Update-Disk -Number %d -ErrorAction Stop
$partition = Get-Partition -DiskNumber %d | Where-Object {
  $_.GptType -eq '{c12a7328-f81f-11d2-ba4b-00a0c93ec93b}'
} | Select-Object -First 1
if (-not $partition) { throw 'The selected USB does not contain a Miner Dash setup partition.' }
$assigned = $false
if (-not $partition.DriveLetter) {
  $partition | Add-PartitionAccessPath -AssignDriveLetter -ErrorAction Stop
  $partition = Get-Partition -DiskNumber %d -PartitionNumber $partition.PartitionNumber
  $assigned = $true
}
try {
  $path = "$($partition.DriveLetter):\minerdash-provision.conf"
  [IO.File]::WriteAllBytes($path, [Convert]::FromBase64String('%s'))
} finally {
  if ($assigned) {
    Remove-PartitionAccessPath -DiskNumber %d -PartitionNumber $partition.PartitionNumber -AccessPath "$($partition.DriveLetter):\" -ErrorAction SilentlyContinue
  }
}
`, device.DiskNumber, device.SizeBytes, device.DiskNumber, device.DiskNumber, device.DiskNumber, encoded, device.DiskNumber)
	if output, err := runUSBPowerShell(script); err != nil {
		return fmt.Errorf("write controller pairing information: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func runUSBPowerShell(script string) ([]byte, error) {
	var encoded bytes.Buffer
	for _, character := range []rune(script) {
		if err := binary.Write(&encoded, binary.LittleEndian, uint16(character)); err != nil {
			return nil, err
		}
	}
	command := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(encoded.Bytes()))
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return stderr.Bytes(), err
	}
	return stdout.Bytes(), nil
}
