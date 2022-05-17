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

package v1

import (
	"fmt"
	"sort"

	"github.com/rancher-sandbox/elemental/pkg/constants"
	"k8s.io/mount-utils"
)

const (
	GPT   = "gpt"
	BIOS  = "bios"
	MSDOS = "msdos"
	EFI   = "efi"
)

// Config is the struct that includes basic and generic configuration of elemental binary runtime.
// It mostly includes the interfaces used around many methods in elemental code
type Config struct {
	Logger                    Logger
	Fs                        FS
	Mounter                   mount.Interface
	Runner                    Runner
	Syscall                   SyscallInterface
	CloudInitRunner           CloudInitRunner
	Luet                      LuetInterface
	Client                    HTTPClient
	Cosign                    bool         `yaml:"cosign,omitempty" mapstructure:"cosign"`
	CosignPubKey              string       `yaml:"cosign-key,omitempty" mapstructure:"cosign-key"`
	LocalImage                bool         `yaml:"local,omitempty" mapstructure:"local"`
	Repos                     []Repository `yaml:"repositories,omitempty" mapstructure:"repositories"`
	Arch                      string       `yaml:"arch,omitempty" mapstructure:"arch"`
	SquashFsCompressionConfig []string     `yaml:"squash-compression,omitempty" mapstructure:"SquashFsCompressionConfig"`
}

type RunConfig struct {
	Strict         bool     `yaml:"strict,omitempty" mapstructure:"strict"`
	NoVerify       bool     `yaml:"no-verify,omitempty" mapstructure:"no-verify"`
	Reboot         bool     `yaml:"reboot,omitempty" mapstructure:"reboot"`
	PowerOff       bool     `yaml:"poweroff,omitempty" mapstructure:"poweroff"`
	CloudInitPaths []string `yaml:"cloud-init-paths,omitempty" mapstructure:"cloud-init-paths"`
	EjectCD        bool     `yaml:"eject-cd,omitempty" mapstructure:"eject-cd"`

	Config
}

// InstallSpec struct represents all the installation action details
type InstallSpec struct {
	Target       string       `yaml:"target,omitempty" mapstructure:"target"`
	Firmware     string       `yaml:"firmware,omitempty" mapstructure:"firmware"`
	PartTable    string       `yaml:"part-table,omitempty" mapstructure:"part-table"`
	Partitions   PartitionMap `yaml:"partitions,omitempty" mapstructure:"partitions"`
	NoFormat     bool         `yaml:"no-format,omitempty" mapstructure:"no-format"`
	Force        bool         `yaml:"force,omitempty" mapstructure:"force"`
	CloudInit    string       `yaml:"cloud-init,omitempty" mapstructure:"cloud-init"`
	Iso          string       `yaml:"iso,omitempty" mapstructure:"iso"`
	GrubDefEntry string       `yaml:"grub-default-entry,omitempty" mapstructure:"grub-default-entry"`
	GrubTty      string       `yaml:"grub-tty,omitempty" mapstructure:"grub-tty"`
	ActiveImg    Image        `yaml:"system,omitempty" mapstructure:"system"`
	RecoveryImg  Image        `yaml:"recovery-system,omitempty" mapstructure:"recovery-system"`
	PassiveImg   Image
	GrubConf     string
}

func passiveLabel(data interface{}) (string, error) {
	m, ok := data.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("cannot unmarshal, unexpected format %+v", data)
	}
	raw, ok := m["passive-label"]
	if !ok {
		// No passive-label tag, continue decoding
		return "", nil
	}
	label, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("invalid 'passive-label' type, unexpected format")
	}
	return label, nil
}

// Manually unmarshals passive-label to PassiveImg.Label
func (i *InstallSpec) CustomUnmarshal(data interface{}) (bool, error) {
	label, err := passiveLabel(data)
	if err != nil {
		return true, err
	}
	i.PassiveImg.Label = label
	return true, nil
}

// ResetSpec struct represents all the reset action details
type ResetSpec struct {
	FormatPersistent bool   `yaml:"reset-persistent,omitempty" mapstructure:"reset-persistent"`
	GrubDefEntry     string `yaml:"grub-default-entry,omitempty" mapstructure:"grub-default-entry"`
	GrubTty          string `yaml:"grub-tty,omitempty" mapstructure:"grub-tty"`
	ActiveImg        Image  `yaml:"system,omitempty" mapstructure:"system"`
	PassiveImg       Image
	Partitions       PartitionMap
	Target           string
	Efi              bool
	GrubConf         string
}

// Manually unmarshals passive-label to PassiveImg.Label
func (r *ResetSpec) CustomUnmarshal(data interface{}) (bool, error) {
	label, err := passiveLabel(data)
	if err != nil {
		return true, err
	}
	r.PassiveImg.Label = label
	return true, nil
}

type UpgradeSpec struct {
	RecoveryUpgrade    bool   `yaml:"upgrade-recovery,omitempty" mapstructure:"upgrade-recovery"`
	ActiveImg          Image  `yaml:"system,omitempty" mapstructure:"system"`
	RecoveryImg        Image  `yaml:"recovery-system,omitempty" mapstructure:"recovery-system"`
	PassiveLabel       string `yaml:"passive-label,omitempty" mapstructure:"passive-label"`
	Partitions         PartitionMap
	BootedFromRecovery bool
	SquashedRecovery   bool
}

// Partition struct represents a partition with its commonly configurable values, size in MiB
type Partition struct {
	Name       string
	Label      string   `yaml:"label,omitempty" mapstructure:"label"`
	Size       uint     `yaml:"size,omitempty" mapstructure:"size"`
	FS         string   `yaml:"fs,omitempty" mapstrcuture:"fs"`
	Flags      []string `yaml:"flags,omitempty" mapstrcuture:"flags"`
	MountPoint string
	Path       string
	Disk       string
}

type PartitionList []*Partition

// GetPartitionMap gets a partition map mapped by partition name. Partitions
// without a default name and not matching any default label are ignored.
func (pl PartitionList) GetPartitionMap() PartitionMap {
	pm := PartitionMap{}
	for _, part := range pl {
		if part == nil {
			continue
		}
		for k, v := range constants.GetPartitionDefaultLabels() {
			if part.Name == k || part.Label == v {
				pm[k] = part
			}
		}
	}
	return pm
}

// GetByName gets a partitions by its name from the PartitionList
func (pl PartitionList) GetByName(name string) *Partition {
	for _, p := range pl {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// GetByLabel gets a partition by its label from the PartitionList
func (pl PartitionList) GetByLabel(label string) *Partition {
	for _, p := range pl {
		if p.Label == label {
			return p
		}
	}
	return nil
}

type PartitionMap map[string]*Partition

// validName checks if the given partition name is valid within the unmarshaling scope
func (pm PartitionMap) validName(name string) bool {
	for _, n := range constants.GetCustomizablePartitions() {
		if n == name {
			return true
		}
	}
	return false
}

// CustomUnmarshal only checks the keys of the PartitionMap are valid, non valid ones are ignored
func (pm PartitionMap) CustomUnmarshal(data interface{}) (bool, error) {
	m, ok := data.(map[string]interface{})
	if !ok {
		return true, fmt.Errorf("cannot unmarshal to PartitionMap, unexpected format %+v", data)
	}
	for k := range m {
		if !pm.validName(k) {
			// Removing the invalid entry causes to completely ignore it
			delete(m, k)
		}
	}
	return true, nil
}

// OrderedByLayoutPartitions sorts partitions according to the default layout
func (pm PartitionMap) OrderedByLayoutPartitions() PartitionList {
	var part *Partition
	var present bool

	partitions := PartitionList{}
	for _, name := range constants.GetPartitionsOrder() {
		if part, present = pm[name]; present {
			partitions = append(partitions, part)
		}
	}
	return partitions
}

// OrderedByMountPointPartitions sorts partitions according to its mountpoint, ignores partitions
// with an empty mountpoint, these are excluded
func (pm PartitionMap) OrderedByMountPointPartitions(descending bool) PartitionList {
	mountPointKeys := map[string]string{}
	mountPoints := []string{}
	partitions := PartitionList{}

	for k, v := range pm {
		if v.MountPoint != "" {
			mountPointKeys[v.MountPoint] = k
			mountPoints = append(mountPoints, v.MountPoint)
		}
	}

	if descending {
		sort.Sort(sort.Reverse(sort.StringSlice(mountPoints)))
	} else {
		sort.Strings(mountPoints)
	}

	for _, mnt := range mountPoints {
		partitions = append(partitions, pm[mountPointKeys[mnt]])
	}
	return partitions
}

// Image struct represents a file system image with its commonly configurable values, size in MiB
type Image struct {
	File       string
	Label      string       `yaml:"label,omitempty" mapstructure:"label"`
	Size       uint         `yaml:"size,omitempty" mapstructure:"size"`
	FS         string       `yaml:"fs,omitempty" mapstructure:"fs"`
	Source     *ImageSource `yaml:"uri,omitempty" mapstructure:"uri"`
	MountPoint string
	LoopDevice string
}

type ImageMap map[string]*Image

// ImageMap setters and getters are just shortcut for accesses using constants

func (im ImageMap) SetActive(img *Image) {
	im[constants.ActiveImgName] = img
}

func (im ImageMap) SetPassive(img *Image) {
	im[constants.PassiveImgName] = img
}

func (im ImageMap) SetRecovery(img *Image) {
	im[constants.RecoveryImgName] = img
}

func (im ImageMap) GetActive() *Image {
	return im[constants.ActiveImgName]
}

func (im ImageMap) GetPassive() *Image {
	return im[constants.PassiveImgName]
}

func (im ImageMap) GetRecovery() *Image {
	return im[constants.RecoveryImgName]
}

// LiveISO represents the configurations needed for a live ISO image
type LiveISO struct {
	RootFS      []string `yaml:"rootfs,omitempty" mapstructure:"rootfs"`
	UEFI        []string `yaml:"uefi,omitempty" mapstructure:"uefi"`
	Image       []string `yaml:"image,omitempty" mapstructure:"image"`
	Label       string   `yaml:"label,omitempty" mapstructure:"label"`
	BootCatalog string   `yaml:"boot_catalog,omitempty" mapstructure:"boot_catalog"`
	BootFile    string   `yaml:"boot_file,omitempty" mapstructure:"boot_file"`
	HybridMBR   string   `yaml:"hybrid_mbr,omitempty" mapstructure:"hybrid_mbr,omitempty"`
}

// Repository represents the basic configuration for a package repository
type Repository struct {
	Name     string `yaml:"name,omitempty" mapstructure:"name"`
	Priority int    `yaml:"priority,omitempty" mapstructure:"priority"`
	URI      string `yaml:"uri,omitempty" mapstructure:"uri"`
	Type     string `yaml:"type,omitempty" mapstructure:"type"`
	Arch     string `yaml:"arch,omitempty" mapstructure:"arch"`
}

// BuildConfig represents the config we need for building isos, raw images, artifacts
type BuildConfig struct {
	ISO     *LiveISO                     `yaml:"iso,omitempty" mapstructure:"iso"`
	Date    bool                         `yaml:"date,omitempty" mapstructure:"date"`
	Name    string                       `yaml:"name,omitempty" mapstructure:"name"`
	RawDisk map[string]*RawDiskArchEntry `yaml:"raw_disk,omitempty" mapstructure:"raw_disk"`
	OutDir  string                       `yaml:"output,omitempty" mapstructure:"output"`
	// Generic runtime configuration
	Config `yaml:",inline" mapstructure:",squash"`
}

// RawDiskArchEntry represents an arch entry in raw_disk
type RawDiskArchEntry struct {
	Repositories []Repository     `yaml:"repo,omitempty"`
	Packages     []RawDiskPackage `yaml:"packages,omitempty"`
}

// RawDiskPackage represents a package entry for raw_disk, with a package name and a target to install to
type RawDiskPackage struct {
	Name   string `yaml:"name,omitempty"`
	Target string `yaml:"target,omitempty"`
}
