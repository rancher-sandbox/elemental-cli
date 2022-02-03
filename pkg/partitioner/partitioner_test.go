/*
Copyright © 2021 SUSE LLC

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

package partitioner_test

import (
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	part "github.com/rancher-sandbox/elemental/pkg/partitioner"
	mocks "github.com/rancher-sandbox/elemental/tests/mocks"
	"github.com/spf13/afero"
	"testing"
)

const printOutput = `BYT;
/dev/loop0:50593792s:loopback:512:512:msdos:Loopback device:;
1:2048s:98303s:96256s:ext4::type=83;
2:98304s:29394943s:29296640s:ext4::boot, type=83;
3:29394944s:45019135s:15624192s:ext4::type=83;
4:45019136s:50331647s:5312512s:ext4::type=83;`

func TestElementalSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Partitioner test suite")
}

var _ = Describe("Partitioner", func() {
	var runner *mocks.TestRunnerV2
	BeforeEach(func() {
		runner = mocks.NewTestRunnerV2()
	})
	Context("Parted tests", func() {
		var pc *part.PartedCall
		BeforeEach(func() {
			pc = part.NewPartedCall("/some/dev", runner)
		})
		It("Write changes does nothing with empty setup", func() {
			pc := part.NewPartedCall("/some/dev", runner)
			_, err := pc.WriteChanges()
			Expect(err).To(BeNil())
		})
		It("Runs complex command", func() {
			cmds := [][]string{{
				"parted", "--script", "--machine", "--", "/some/dev",
				"unit", "s", "mklabel", "gpt", "mkpart", "p.efi", "fat32",
				"2048", "206847", "mkpart", "p.root", "ext4", "206848", "100%",
			}}
			part1 := part.Partition{
				Number: 0, StartS: 2048, SizeS: 204800,
				PLabel: "p.efi", FileSystem: "vfat",
			}
			pc.CreatePartition(&part1)
			part2 := part.Partition{
				Number: 0, StartS: 206848, SizeS: 0,
				PLabel: "p.root", FileSystem: "ext4",
			}
			pc.CreatePartition(&part2)
			pc.WipeTable(true)
			_, err := pc.WriteChanges()
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("Set a new partition label", func() {
			cmds := [][]string{{
				"parted", "--script", "--machine", "--", "/some/dev",
				"unit", "s", "mklabel", "msdos",
			}}
			pc.SetPartitionTableLabel("msdos")
			pc.WipeTable(true)
			_, err := pc.WriteChanges()
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("Creates a new partition", func() {
			cmds := [][]string{{
				"parted", "--script", "--machine", "--", "/some/dev",
				"unit", "s", "mkpart", "p.root", "ext4", "2048", "206847",
			}, {
				"parted", "--script", "--machine", "--", "/some/dev",
				"unit", "s", "mkpart", "p.root", "ext4", "2048", "100%",
			}}
			partition := part.Partition{
				Number: 0, StartS: 2048, SizeS: 204800,
				PLabel: "p.root", FileSystem: "ext4",
			}
			pc.CreatePartition(&partition)
			_, err := pc.WriteChanges()
			Expect(err).To(BeNil())
			partition = part.Partition{
				Number: 0, StartS: 2048, SizeS: 0,
				PLabel: "p.root", FileSystem: "ext4",
			}
			pc.CreatePartition(&partition)
			_, err = pc.WriteChanges()
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("Deletes a partition", func() {
			cmd := []string{
				"parted", "--script", "--machine", "--", "/some/dev",
				"unit", "s", "rm", "1", "rm", "2",
			}
			pc.DeletePartition(1)
			pc.DeletePartition(2)
			_, err := pc.WriteChanges()
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch([][]string{cmd})).To(BeNil())
		})
		It("Set a partition flag", func() {
			cmds := [][]string{{
				"parted", "--script", "--machine", "--", "/some/dev",
				"unit", "s", "set", "1", "flag", "on", "set", "2", "flag", "off",
			}}
			pc.SetPartitionFlag(1, "flag", true)
			pc.SetPartitionFlag(2, "flag", false)
			_, err := pc.WriteChanges()
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("Wipes partition table creating a new one", func() {
			cmd := []string{
				"parted", "--script", "--machine", "--", "/some/dev",
				"unit", "s", "mklabel", "gpt",
			}
			pc.WipeTable(true)
			_, err := pc.WriteChanges()
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch([][]string{cmd})).To(BeNil())
		})
		It("Prints partitin table info", func() {
			cmd := []string{
				"parted", "--script", "--machine", "--", "/some/dev",
				"unit", "s", "print",
			}
			_, err := pc.Print()
			Expect(err).To(BeNil())
			Expect(runner.CmdsMatch([][]string{cmd})).To(BeNil())
		})
		It("Gets last sector of the disk", func() {
			lastSec, _ := pc.GetLastSector(printOutput)
			Expect(lastSec).To(Equal(uint(50593792)))
			_, err := pc.GetLastSector("invalid parted print output")
			Expect(err).NotTo(BeNil())
		})
		It("Gets sector size of the disk", func() {
			secSize, _ := pc.GetSectorSize(printOutput)
			Expect(secSize).To(Equal(uint(512)))
			_, err := pc.GetSectorSize("invalid parted print output")
			Expect(err).NotTo(BeNil())
		})
		It("Gets partition table label", func() {
			label, _ := pc.GetPartitionTableLabel(printOutput)
			Expect(label).To(Equal("msdos"))
			_, err := pc.GetPartitionTableLabel("invalid parted print output")
			Expect(err).NotTo(BeNil())
		})
		It("Gets partitions info of the disk", func() {
			parts := pc.GetPartitions(printOutput)
			Expect(len(parts)).To(Equal(4))
			Expect(parts[1].StartS).To(Equal(uint(98304)))
		})
	})
	Context("Mkfs tests", func() {
		It("Successfully formats a partition with xfs", func() {
			mkfs := part.NewMkfsCall("/some/device", "xfs", "OEM", runner)
			_, err := mkfs.Apply()
			Expect(err).To(BeNil())
			cmds := [][]string{{"mkfs.xfs", "-L", "OEM", "/some/device"}}
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("Successfully formats a partition with vfat", func() {
			mkfs := part.NewMkfsCall("/some/device", "vfat", "EFI", runner)
			_, err := mkfs.Apply()
			Expect(err).To(BeNil())
			cmds := [][]string{{"mkfs.vfat", "-n", "EFI", "/some/device"}}
			Expect(runner.CmdsMatch(cmds)).To(BeNil())
		})
		It("Fails for unsupported filesystem", func() {
			mkfs := part.NewMkfsCall("/some/device", "btrfs", "OEM", runner)
			_, err := mkfs.Apply()
			Expect(err).NotTo(BeNil())
		})
	})
	Context("Disk tests", func() {
		var dev *part.Disk
		var cmds [][]string
		var printCmd []string
		It("Creates a default disk", func() {
			dev = part.NewDisk("/some/device")
		})
		BeforeEach(func() {
			dev = part.NewDisk("/some/device", part.WithRunner(runner), part.WithFS(afero.NewMemMapFs()))
			printCmd = []string{
				"parted", "--script", "--machine", "--", "/some/device",
				"unit", "s", "print",
			}
		})
		Context("Load data without changes", func() {
			BeforeEach(func() {
				cmds = [][]string{printCmd}
				runner.ReturnValue = []byte(printOutput)
			})
			It("Loads disk layout data", func() {
				Expect(dev.Reload()).To(BeNil())
				Expect(dev.String()).To(Equal("/some/device"))
				Expect(dev.GetSectorSize()).To(Equal(uint(512)))
				Expect(dev.GetLastSector()).To(Equal(uint(50593792)))
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Computes available free space", func() {
				Expect(dev.GetFreeSpace()).To(Equal(uint(262145)))
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Checks it has at least 128MB of free space", func() {
				Expect(dev.CheckDiskFreeSpaceMiB(128)).To(Equal(true))
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Checks it has less than 130MB of free space", func() {
				Expect(dev.CheckDiskFreeSpaceMiB(130)).To(Equal(false))
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Get partition label", func() {
				dev.Reload()
				Expect(dev.GetLabel()).To(Equal("msdos"))
			})
		})
		Context("Modify disk", func() {
			It("Fails to create an unsupported partition table label", func() {
				runner.ReturnValue = []byte(printOutput)
				_, err := dev.NewPartitionTable("invalidLabel")
				Expect(err).NotTo(BeNil())
			})
			It("Creates new partition table label", func() {
				cmds = [][]string{{
					"parted", "--script", "--machine", "--", "/some/device",
					"unit", "s", "mklabel", "gpt",
				}, printCmd}
				runner.ReturnValue = []byte(printOutput)
				_, err := dev.NewPartitionTable("gpt")
				Expect(err).To(BeNil())
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Adds a new partition", func() {
				cmds = [][]string{printCmd, {
					"parted", "--script", "--machine", "--", "/some/device",
					"unit", "s", "mkpart", "primary", "ext4", "50331648", "100%",
					"set", "5", "boot", "on",
				}, printCmd}
				runner.ReturnValue = []byte(printOutput)
				num, err := dev.AddPartition(0, "ext4", "ignored", "boot")
				Expect(err).To(BeNil())
				Expect(num).To(Equal(5))
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Fails to a new partition if there is not enough space available", func() {
				cmds = [][]string{printCmd}
				runner.ReturnValue = []byte(printOutput)
				_, err := dev.AddPartition(130, "ext4", "ignored")
				Expect(err).NotTo(BeNil())
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Finds device for a given partition number", func() {
				runner.ReturnValue = []byte("/some/device4 part")
				cmds = [][]string{
					{"udevadm", "settle"},
					{"lsblk", "-ltnpo", "name,type", "/some/device"},
				}
				Expect(dev.FindPartitionDevice(4)).To(Equal("/some/device4"))
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Finds device of partition and requires a retry", func() {
				triesCount := 1
				runFunc := func(cmd string, args ...string) ([]byte, error) {
					switch cmd {
					case "lsblk":
						if triesCount > 0 {
							triesCount--
							return []byte{}, errors.New("Fake error")
						}
						return []byte("/some/device4 part"), nil
					default:
						return []byte{}, nil
					}
				}
				runner.SideEffect = runFunc
				cmds = [][]string{
					{"udevadm", "settle"},
					{"lsblk", "-ltnpo", "name,type", "/some/device"},
					{"udevadm", "settle"},
					{"lsblk", "-ltnpo", "name,type", "/some/device"},
				}
				Expect(dev.FindPartitionDevice(4)).To(Equal("/some/device4"))
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Formats a partition", func() {
				cmds = [][]string{
					{"udevadm", "settle"},
					{"lsblk", "-ltnpo", "name,type", "/some/device"},
					{"mkfs.xfs", "-L", "OEM", "/some/device4"},
				}
				runner.ReturnValue = []byte("/some/device4 part")
				_, err := dev.FormatPartition(4, "xfs", "OEM")
				Expect(err).To(BeNil())
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Clears filesystem header from a partition", func() {
				cmds = [][]string{
					{"udevadm", "settle"},
					{"lsblk", "-ltnpo", "name,type", "/some/device"},
					{"wipefs", "--all", "/some/device1"},
				}
				runner.ReturnValue = []byte("/some/device1 part")
				Expect(dev.WipeFsOnPartition(1)).To(BeNil())
				Expect(runner.CmdsMatch(cmds)).To(BeNil())
			})
			It("Fails while removing file system header", func() {
				runner.ReturnValue = []byte("/some/device1 part")
				runner.ReturnError = errors.New("some error")
				Expect(dev.WipeFsOnPartition(1)).NotTo(BeNil())
			})
			Context("Expanding partitions", func() {
				var fileSystem string
				BeforeEach(func() {
					cmds = [][]string{
						printCmd, {
							"parted", "--script", "--machine", "--", "/some/device",
							"unit", "s", "rm", "4", "mkpart", "primary", "", "45019136", "100%",
						}, printCmd, {"udevadm", "settle"},
						{"lsblk", "-ltnpo", "name,type", "/some/device"},
						{"blkid", "/some/device4", "-s", "TYPE", "-o", "value"},
					}
					runFunc := func(cmd string, args ...string) ([]byte, error) {
						switch cmd {
						case "parted":
							return []byte(printOutput), nil
						case "lsblk":
							return []byte("/some/device4 part"), nil
						case "blkid":
							return []byte(fileSystem), nil
						default:
							return []byte{}, nil
						}
					}
					runner.SideEffect = runFunc
				})
				It("Expands ext4 partition", func() {
					extCmds := [][]string{
						{"e2fsck", "-fy", "/some/device4"}, {"resize2fs", "/some/device4"},
					}
					fileSystem = "ext4"
					_, err := dev.ExpandLastPartition(0)
					Expect(err).To(BeNil())
					Expect(runner.CmdsMatch(append(cmds, extCmds...))).To(BeNil())
				})
				It("Expands xfs partition", func() {
					xfsCmds := [][]string{
						{"mount", "-t", "xfs"}, {"xfs_growfs"}, {"umount"},
					}
					fileSystem = "xfs"
					_, err := dev.ExpandLastPartition(0)
					Expect(err).To(BeNil())
					Expect(runner.CmdsMatch(append(cmds, xfsCmds...))).To(BeNil())
				})
			})
		})
	})
})
