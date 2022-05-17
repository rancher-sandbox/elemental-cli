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

package config

import (
	"fmt"
	"path/filepath"

	"github.com/rancher-sandbox/elemental/pkg/cloudinit"
	"github.com/rancher-sandbox/elemental/pkg/constants"
	"github.com/rancher-sandbox/elemental/pkg/http"
	"github.com/rancher-sandbox/elemental/pkg/luet"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
	"github.com/twpayne/go-vfs"
	"k8s.io/mount-utils"
)

const (
	ESP  = "esp"
	BIOS = "bios_grub"
	BOOT = "boot"
)

type GenericOptions func(a *v1.Config) error

func WithFs(fs v1.FS) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.Fs = fs
		return nil
	}
}

func WithLogger(logger v1.Logger) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.Logger = logger
		return nil
	}
}

func WithSyscall(syscall v1.SyscallInterface) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.Syscall = syscall
		return nil
	}
}

func WithMounter(mounter mount.Interface) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.Mounter = mounter
		return nil
	}
}

func WithRunner(runner v1.Runner) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.Runner = runner
		return nil
	}
}

func WithClient(client v1.HTTPClient) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.Client = client
		return nil
	}
}

func WithCloudInitRunner(ci v1.CloudInitRunner) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.CloudInitRunner = ci
		return nil
	}
}

func WithLuet(luet v1.LuetInterface) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.Luet = luet
		return nil
	}
}

func WithArch(arch string) func(r *v1.Config) error {
	return func(r *v1.Config) error {
		r.Arch = arch
		return nil
	}
}

func NewConfig(opts ...GenericOptions) *v1.Config {
	log := v1.NewLogger()
	//TODO set arch dynamically to the current arch
	c := &v1.Config{
		Fs:                        vfs.OSFS,
		Logger:                    log,
		Syscall:                   &v1.RealSyscall{},
		Client:                    http.NewClient(),
		Repos:                     []v1.Repository{},
		Arch:                      "x86_64",
		SquashFsCompressionConfig: constants.GetDefaultSquashfsCompressionOptions(),
	}
	for _, o := range opts {
		err := o(c)
		if err != nil {
			return nil
		}
	}

	// delay runner creation after we have run over the options in case we use WithRunner
	if c.Runner == nil {
		c.Runner = &v1.RealRunner{Logger: c.Logger}
	}

	// Now check if the runner has a logger inside, otherwise point our logger into it
	// This can happen if we set the WithRunner option as that doesn't set a logger
	if c.Runner.GetLogger() == nil {
		c.Runner.SetLogger(c.Logger)
	}

	// Delay the yip runner creation, so we set the proper logger instead of blindly setting it to the logger we create
	// at the start of NewRunConfig, as WithLogger can be passed on init, and that would result in 2 different logger
	// instances, on the config.Logger and the other on config.CloudInitRunner
	if c.CloudInitRunner == nil {
		c.CloudInitRunner = cloudinit.NewYipCloudInitRunner(c.Logger, c.Runner, vfs.OSFS)
	}

	if c.Mounter == nil {
		c.Mounter = mount.New(constants.MountBinary)
	}

	if c.Luet == nil {
		tmpDir := utils.GetTempDir(c, "")
		c.Luet = luet.NewLuet(luet.WithFs(c.Fs), luet.WithLogger(log), luet.WithLuetTempDir(tmpDir))
	}
	return c
}

func NewRunConfig(opts ...GenericOptions) *v1.RunConfig {
	config := NewConfig(opts...)
	r := &v1.RunConfig{
		Config: *config,
	}
	return r
}

// NewInstallSpec returns an InstallSpec struct all based on defaults and basic host checks (e.g. EFI vs BIOS)
func NewInstallSpec(cfg v1.Config) *v1.InstallSpec {
	var firmware string
	var recoveryImg, activeImg, passiveImg v1.Image

	recoveryImgFile := filepath.Join(constants.LiveDir, constants.RecoverySquashFile)

	// Check if current host has EFI firmware
	efiExists, _ := utils.Exists(cfg.Fs, constants.EfiDevice)
	// Check the default ISO installation media is available
	isoRootExists, _ := utils.Exists(cfg.Fs, constants.IsoBaseTree)
	// Check the default ISO recovery installation media is available)
	recoveryExists, _ := utils.Exists(cfg.Fs, recoveryImgFile)

	if efiExists {
		firmware = v1.EFI
	} else {
		firmware = v1.BIOS
	}

	activeImg.Label = constants.ActiveLabel
	activeImg.Size = constants.ImgSize
	activeImg.File = filepath.Join(constants.StateDir, "cOS", constants.ActiveImgFile)
	activeImg.FS = constants.LinuxImgFs
	activeImg.MountPoint = constants.ActiveDir
	if isoRootExists {
		activeImg.Source = v1.NewDirSrc(constants.IsoBaseTree)
	} else {
		activeImg.Source = v1.NewEmptySrc()
	}

	if recoveryExists {
		recoveryImg.Source = v1.NewFileSrc(recoveryImgFile)
		recoveryImg.FS = constants.SquashFs
		recoveryImg.File = filepath.Join(constants.RecoveryDir, "cOS", constants.RecoverySquashFile)
	} else {
		recoveryImg.Source = v1.NewFileSrc(activeImg.File)
		recoveryImg.FS = constants.LinuxImgFs
		recoveryImg.Label = constants.SystemLabel
		recoveryImg.File = filepath.Join(constants.RecoveryDir, "cOS", constants.RecoveryImgFile)
	}

	passiveImg = v1.Image{
		File:   filepath.Join(constants.StateDir, "cOS", constants.PassiveImgFile),
		Label:  constants.PassiveLabel,
		Source: v1.NewFileSrc(activeImg.File),
		FS:     constants.LinuxImgFs,
	}

	return &v1.InstallSpec{
		Firmware:     firmware,
		PartTable:    v1.GPT,
		Partitions:   NewInstallParitionMap(),
		GrubDefEntry: constants.GrubDefEntry,
		GrubConf:     constants.GrubConf,
		Tty:          constants.DefaultTty,
		ActiveImg:    activeImg,
		RecoveryImg:  recoveryImg,
		PassiveImg:   passiveImg,
	}
}

func AddFirmwarePartitions(i *v1.InstallSpec) error {
	if i.Partitions == nil {
		return fmt.Errorf("nil partitions map")
	}
	if i.Firmware == v1.EFI && i.PartTable == v1.GPT {
		i.Partitions[constants.EfiPartName] = &v1.Partition{
			Label:      constants.EfiLabel,
			Size:       constants.EfiSize,
			Name:       constants.EfiPartName,
			FS:         constants.EfiFs,
			MountPoint: constants.EfiDir,
			Flags:      []string{ESP},
		}
	} else if i.Firmware == v1.BIOS && i.PartTable == v1.GPT {
		i.Partitions[constants.BiosPartName] = &v1.Partition{
			Label:      "",
			Size:       constants.BiosSize,
			Name:       constants.BiosPartName,
			FS:         "",
			MountPoint: "",
			Flags:      []string{BIOS},
		}
	} else {
		statePart, ok := i.Partitions[constants.StatePartName]
		if !ok {
			return fmt.Errorf("nil state partition")
		}
		statePart.Flags = []string{BOOT}
	}
	return nil
}

func NewInstallParitionMap() v1.PartitionMap {
	partitions := v1.PartitionMap{}
	partitions[constants.OEMPartName] = &v1.Partition{
		Label:      constants.OEMLabel,
		Size:       constants.OEMSize,
		Name:       constants.OEMPartName,
		FS:         constants.LinuxFs,
		MountPoint: constants.OEMDir,
		Flags:      []string{},
	}

	partitions[constants.RecoveryPartName] = &v1.Partition{
		Label:      constants.RecoveryLabel,
		Size:       constants.RecoverySize,
		Name:       constants.RecoveryPartName,
		FS:         constants.LinuxFs,
		MountPoint: constants.RecoveryDir,
		Flags:      []string{},
	}

	partitions[constants.StatePartName] = &v1.Partition{
		Label:      constants.StateLabel,
		Size:       constants.StateSize,
		Name:       constants.StatePartName,
		FS:         constants.LinuxFs,
		MountPoint: constants.StateDir,
		Flags:      []string{},
	}

	partitions[constants.PersistentPartName] = &v1.Partition{
		Label:      constants.PersistentLabel,
		Size:       constants.PersistentSize,
		Name:       constants.PersistentPartName,
		FS:         constants.LinuxFs,
		MountPoint: constants.PersistentDir,
		Flags:      []string{},
	}
	return partitions
}

// NewUpgradeSpec returns an UpgradeSpec struct all based on defaults and current host state
func NewUpgradeSpec(cfg v1.Config) (*v1.UpgradeSpec, error) {
	var recLabel, recFs, recMnt string

	parts, err := utils.GetAllPartitions()
	if err != nil {
		return nil, fmt.Errorf("could not read host partitions")
	}
	partitionMap := parts.GetPartitionMap()

	recPart, ok := partitionMap[constants.RecoveryPartName]
	if !ok {
		return nil, fmt.Errorf("recovery partition not found")
	} else if recPart.MountPoint == "" {
		recPart.MountPoint = constants.RecoveryDir
	}

	statePart, ok := partitionMap[constants.StatePartName]
	if !ok {
		return nil, fmt.Errorf("state partition not found")
	} else if statePart.MountPoint == "" {
		statePart.MountPoint = constants.StateDir
	}

	// TODO find a way to pre-load current state values such as SystemLabel
	bootedRec := utils.BootedFrom(cfg.Runner, constants.RecoverySquashFile) || utils.BootedFrom(cfg.Runner, constants.SystemLabel)
	squashedRec, err := utils.HasSquashedRecovery(&cfg, partitionMap[constants.RecoveryPartName])
	if err != nil {
		return nil, fmt.Errorf("failed checking for squashed recovery")
	}

	active := v1.Image{
		File:       filepath.Join(statePart.MountPoint, "cOS", constants.TransitionImgFile),
		Size:       constants.ImgSize,
		Label:      constants.ActiveLabel,
		FS:         constants.LinuxImgFs,
		MountPoint: constants.TransitionDir,
		Source:     v1.NewEmptySrc(), //TODO apply defaults if any
	}

	if squashedRec {
		recFs = constants.SquashFs
	} else {
		recLabel = constants.SystemLabel
		recFs = constants.LinuxImgFs
		recMnt = constants.TransitionDir
	}
	recovery := v1.Image{
		File:       filepath.Join(recPart.MountPoint, "cOS", constants.TransitionImgFile),
		Size:       constants.ImgSize,
		Label:      recLabel,
		FS:         recFs,
		MountPoint: recMnt,
		Source:     v1.NewEmptySrc(), //TODO apply defaults if any
	}

	return &v1.UpgradeSpec{
		BootedFromRecovery: bootedRec,
		SquashedRecovery:   squashedRec,
		ActiveImg:          active,
		RecoveryImg:        recovery,
		Partitions:         partitionMap,
	}, nil
}

// NewResetSpec returns a ResetSpec struct all based on defaults and current host state
func NewResetSpec(cfg v1.Config) (*v1.ResetSpec, error) {
	var imgSource *v1.ImageSource

	//TODO find a way to pre-load current state values such as labels
	if !utils.BootedFrom(cfg.Runner, constants.RecoverySquashFile) &&
		!utils.BootedFrom(cfg.Runner, constants.SystemLabel) {
		return nil, fmt.Errorf("reset can only be called from the recovery system")
	}

	efiExists, _ := utils.Exists(cfg.Fs, constants.EfiDevice)

	parts, err := utils.GetAllPartitions()
	if err != nil {
		return nil, fmt.Errorf("could not read host partitions")
	}
	partitions := parts.GetPartitionMap()

	// We won't do anything with the recovery partition
	// removing it so we can easily loop to mount and unmount
	delete(partitions, constants.RecoveryPartName)

	if efiExists {
		partEfi, ok := partitions[constants.EfiPartName]
		if !ok {
			cfg.Logger.Errorf("EFI partition not found!")
			return nil, err
		}
		if partEfi.MountPoint == "" {
			partEfi.MountPoint = constants.EfiDir
		}
		partEfi.Name = constants.EfiPartName
	}

	partState, ok := partitions[constants.StatePartName]
	if !ok {
		cfg.Logger.Error("state partition not found")
		return nil, err
	}
	if partState.MountPoint == "" {
		partState.MountPoint = constants.StateDir
	}
	partState.Name = constants.StatePartName
	target := partState.Disk

	// OEM partition is not a hard requirement
	partOEM, ok := partitions[constants.OEMPartName]
	if ok {
		if partOEM.MountPoint == "" {
			partOEM.MountPoint = constants.OEMDir
		}
		partOEM.Name = constants.OEMPartName
	} else {
		cfg.Logger.Warnf("no OEM partition found")
	}

	// Persistent partition is not a hard requirement
	partPersistent, ok := partitions[constants.PersistentPartName]
	if ok {
		if partPersistent.MountPoint == "" {
			partPersistent.MountPoint = constants.PersistentDir
		}
		partPersistent.Name = constants.PersistentPartName
	} else {
		cfg.Logger.Warnf("no Persistent partition found")
	}

	recoveryImg := filepath.Join(constants.RunningStateDir, "cOS", constants.RecoveryImgFile)
	if exists, _ := utils.Exists(cfg.Fs, recoveryImg); exists {
		imgSource = v1.NewFileSrc(recoveryImg)
	} else if exists, _ = utils.Exists(cfg.Fs, constants.IsoBaseTree); exists {
		imgSource = v1.NewDirSrc(constants.IsoBaseTree)
	} else {
		imgSource = v1.NewEmptySrc()
	}

	activeFile := filepath.Join(partState.MountPoint, "cOS", constants.ActiveImgFile)
	return &v1.ResetSpec{
		Target:       target,
		Partitions:   partitions,
		Efi:          efiExists,
		GrubDefEntry: constants.GrubDefEntry,
		GrubConf:     constants.GrubConf,
		Tty:          constants.DefaultTty,
		ActiveImg: v1.Image{
			Label:      constants.ActiveLabel,
			Size:       constants.ImgSize,
			File:       activeFile,
			FS:         constants.LinuxImgFs,
			Source:     imgSource,
			MountPoint: constants.ActiveDir,
		},
		PassiveImg: v1.Image{
			File:   filepath.Join(partState.MountPoint, "cOS", constants.PassiveImgFile),
			Label:  constants.PassiveLabel,
			Source: v1.NewFileSrc(activeFile),
			FS:     constants.LinuxImgFs,
		},
	}, nil
}

func NewISO() *v1.LiveISO {
	return &v1.LiveISO{
		Label:       constants.ISOLabel,
		UEFI:        constants.GetDefaultISOUEFI(),
		Image:       constants.GetDefaultISOImage(),
		HybridMBR:   constants.IsoHybridMBR,
		BootFile:    constants.IsoBootFile,
		BootCatalog: constants.IsoBootCatalog,
	}
}

func NewBuildConfig(opts ...GenericOptions) *v1.BuildConfig {
	b := &v1.BuildConfig{
		Config: *NewConfig(opts...),
		ISO:    NewISO(),
		Name:   constants.BuildImgName,
	}
	return b
}
