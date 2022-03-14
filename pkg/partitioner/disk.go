/*
Copyright © 2022 SUSE LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package partitioner

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
	"github.com/twpayne/go-vfs"
)

const (
	partitionTries = 10
)

type Disk struct {
	device  string
	sectorS uint
	lastS   uint
	parts   []Partition
	label   string
	runner  v1.Runner
	fs      v1.FS
	logger  v1.Logger
}

func MiBToSectors(size uint, sectorSize uint) uint {
	return size * 1048576 / sectorSize
}

func NewDisk(device string, opts ...DiskOptions) *Disk {
	dev := &Disk{device: device}

	for _, opt := range opts {
		if err := opt(dev); err != nil {
			return nil
		}
	}

	if dev.runner == nil {
		dev.runner = &v1.RealRunner{}
	}

	if dev.fs == nil {
		dev.fs = vfs.OSFS
	}

	if dev.logger == nil {
		dev.logger = v1.NewLogger()
	}

	return dev
}

// FormatDevice formats a block device with the given parameters
func FormatDevice(runner v1.Runner, device string, fileSystem string, label string, opts ...string) error {
	mkfs := MkfsCall{fileSystem: fileSystem, label: label, customOpts: opts, dev: device, runner: runner}
	_, err := mkfs.Apply()
	return err
}

func (dev Disk) String() string {
	return dev.device
}

func (dev Disk) GetSectorSize() uint {
	return dev.sectorS
}

func (dev Disk) GetLastSector() uint {
	return dev.lastS
}

func (dev Disk) GetLabel() string {
	return dev.label
}

func (dev Disk) Exists() bool {
	exists, _ := utils.Exists(dev.fs, dev.device)
	return exists
}

func (dev *Disk) Reload() error {
	pc := NewPartedCall(dev.String(), dev.runner)
	prnt, err := pc.Print()
	if err != nil {
		return err
	}

	sectorS, err := pc.GetSectorSize(prnt)
	if err != nil {
		return err
	}
	lastS, err := pc.GetLastSector(prnt)
	if err != nil {
		return err
	}
	label, err := pc.GetPartitionTableLabel(prnt)
	if err != nil {
		return err
	}
	partitions := pc.GetPartitions(prnt)
	dev.sectorS = sectorS
	dev.lastS = lastS
	dev.parts = partitions
	dev.label = label
	return nil
}

// Size is expressed in MiB here
func (dev *Disk) CheckDiskFreeSpaceMiB(minSpace uint) bool {
	freeS, err := dev.GetFreeSpace()
	if err != nil {
		dev.logger.Warnf("Could not calculate disk free space")
		return false
	}
	minSec := MiBToSectors(minSpace, dev.sectorS)

	return freeS >= minSec
}

func (dev *Disk) GetFreeSpace() (uint, error) {
	//Check we have loaded partition table data
	if dev.sectorS == 0 {
		err := dev.Reload()
		if err != nil {
			dev.logger.Errorf("Failed analyzing disk: %v\n", err)
			return 0, err
		}
	}

	return dev.computeFreeSpace(), nil
}

func (dev Disk) computeFreeSpace() uint {
	if len(dev.parts) > 0 {
		lastPart := dev.parts[len(dev.parts)-1]
		return dev.lastS - (lastPart.StartS + lastPart.SizeS - 1)
	}
	// First partition starts at a 1MiB offset
	return dev.lastS - (1*1024*1024/dev.sectorS - 1)
}

func (dev Disk) computeFreeSpaceWithoutLast() uint {
	if len(dev.parts) > 1 {
		part := dev.parts[len(dev.parts)-2]
		return dev.lastS - (part.StartS + part.SizeS - 1)
	}
	// Assume first partitions is alined to 1MiB
	return dev.lastS - (1024*1024/dev.sectorS - 1)
}

func (dev *Disk) NewPartitionTable(label string) (string, error) {
	match, _ := regexp.MatchString("msdos|gpt", label)
	if !match {
		return "", errors.New("Invalid partition table type, only msdos and gpt are supported")
	}
	pc := NewPartedCall(dev.String(), dev.runner)
	pc.SetPartitionTableLabel(label)
	pc.WipeTable(true)
	out, err := pc.WriteChanges()
	if err != nil {
		return out, err
	}
	err = dev.Reload()
	if err != nil {
		dev.logger.Errorf("Failed analyzing disk: %v\n", err)
		return "", err
	}
	return out, nil
}

//Size is expressed in MiB here
func (dev *Disk) AddPartition(size uint, fileSystem string, pLabel string, flags ...string) (int, error) {
	pc := NewPartedCall(dev.String(), dev.runner)

	//Check we have loaded partition table data
	if dev.sectorS == 0 {
		err := dev.Reload()
		if err != nil {
			dev.logger.Errorf("Failed analyzing disk: %v\n", err)
			return 0, err
		}
	}

	pc.SetPartitionTableLabel(dev.label)

	var partNum int
	var startS uint
	if len(dev.parts) > 0 {
		lastP := len(dev.parts) - 1
		partNum = dev.parts[lastP].Number
		startS = dev.parts[lastP].StartS + dev.parts[lastP].SizeS
	} else {
		//First partition is aligned at 1MiB
		startS = 1024 * 1024 / dev.sectorS
	}

	size = MiBToSectors(size, dev.sectorS)
	freeS := dev.computeFreeSpace()
	if size > freeS {
		return 0, fmt.Errorf("not enough free space in disk. Required: %d sectors; Available %d sectors", size, freeS)
	}

	partNum++
	var part = Partition{
		Number:     partNum,
		StartS:     startS,
		SizeS:      size,
		PLabel:     pLabel,
		FileSystem: fileSystem,
	}

	pc.CreatePartition(&part)
	for _, flag := range flags {
		pc.SetPartitionFlag(partNum, flag, true)
	}

	out, err := pc.WriteChanges()
	dev.logger.Debugf("partitioner output: %s", out)
	if err != nil {
		dev.logger.Errorf("Failed creating partition: %v", err)
		return 0, err
	}

	// Reload new partition in dev
	err = dev.Reload()
	if err != nil {
		dev.logger.Errorf("Failed analyzing disk: %v\n", err)
		return 0, err
	}
	return partNum, nil
}

func (dev Disk) FormatPartition(partNum int, fileSystem string, label string) (string, error) {
	pDev, err := dev.FindPartitionDevice(partNum)
	if err != nil {
		return "", err
	}

	mkfs := MkfsCall{fileSystem: fileSystem, label: label, customOpts: []string{}, dev: pDev, runner: dev.runner}
	return mkfs.Apply()
}

func (dev Disk) WipeFsOnPartition(device string) error {
	/*pDev, err := dev.FindPartitionDevice(partNum)
	if err != nil {
		return err
	}*/

	_, err := dev.runner.Run("wipefs", "--all", device)
	return err
}

func (dev Disk) FindPartitionDevice(partNum int) (string, error) {
	re, _ := regexp.Compile(fmt.Sprintf("(?m)^(/.*%d) part$", partNum))

	for tries := 0; tries <= partitionTries; tries++ {
		dev.logger.Debugf("Trying to find the partition device %d of device %s (try number %d)", partNum, dev, tries+1)
		_, _ = dev.runner.Run("udevadm", "settle")
		out, err := dev.runner.Run("lsblk", "-ltnpo", "name,type", dev.device)
		dev.logger.Debugf("Output of lsblk: %s", out)
		if err != nil && tries == (partitionTries-1) {
			dev.logger.Debugf("Error of lsblk: %s", err)
			return "", fmt.Errorf("could not list device partition nodes: %s", out)
		} else if err == nil {
			matched := re.FindStringSubmatch(string(out))
			if matched != nil {
				return matched[1], nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return "", fmt.Errorf("could not find partition device path for partition %d", partNum)
}

//Size is expressed in MiB here
func (dev *Disk) ExpandLastPartition(size uint) (string, error) {
	pc := NewPartedCall(dev.String(), dev.runner)

	//Check we have loaded partition table data
	if dev.sectorS == 0 {
		err := dev.Reload()
		if err != nil {
			dev.logger.Errorf("Failed analyzing disk: %v\n", err)
			return "", err
		}
	}

	pc.SetPartitionTableLabel(dev.label)

	if len(dev.parts) == 0 {
		return "", errors.New("There is no partition to expand")
	}

	part := dev.parts[len(dev.parts)-1]
	if size > 0 {
		size = MiBToSectors(size, dev.sectorS)
		part := dev.parts[len(dev.parts)-1]
		if size < part.SizeS {
			return "", errors.New("Layout plugin can only expand a partition, not shrink it")
		}
		freeS := dev.computeFreeSpaceWithoutLast()
		if size > freeS {
			return "", fmt.Errorf("not enough free space for to expand last partition up to %d sectors", size)
		}
	}
	part.SizeS = size
	pc.DeletePartition(part.Number)
	pc.CreatePartition(&part)
	out, err := pc.WriteChanges()
	if err != nil {
		return out, err
	}
	err = dev.Reload()
	if err != nil {
		return "", err
	}
	pDev, err := dev.FindPartitionDevice(part.Number)
	if err != nil {
		return "", err
	}
	return dev.expandFilesystem(pDev)
}

func (dev Disk) expandFilesystem(device string) (string, error) {
	var out []byte
	var err error

	fs, _ := dev.runner.Run("blkid", device, "-s", "TYPE", "-o", "value")

	switch strings.TrimSpace(string(fs)) {
	case "ext2", "ext3", "ext4":
		out, err = dev.runner.Run("e2fsck", "-fy", device)
		if err != nil {
			return string(out), err
		}
		out, err = dev.runner.Run("resize2fs", device)

		if err != nil {
			return string(out), err
		}
	case "xfs":
		// to grow an xfs fs it needs to be mounted :/
		tmpDir, err := utils.TempDir(dev.fs, "", "yip")
		defer func(fs v1.FS, path string) {
			_ = fs.RemoveAll(path)
		}(dev.fs, tmpDir)

		if err != nil {
			return string(out), err
		}
		out, err = dev.runner.Run("mount", "-t", "xfs", device, tmpDir)
		if err != nil {
			return string(out), err
		}
		_, err = dev.runner.Run("xfs_growfs", tmpDir)
		if err != nil {
			// If we error out, try to umount the dir to not leave it hanging
			out, err2 := dev.runner.Run("umount", tmpDir)
			if err2 != nil {
				return string(out), err2
			}
			return string(out), err
		}
		out, err = dev.runner.Run("umount", tmpDir)
		if err != nil {
			return string(out), err
		}
	default:
		return "", fmt.Errorf("could not find filesystem for %s, not resizing the filesystem", device)
	}

	return "", nil
}
