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
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/sanity-io/litter"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/mount-utils"

	"github.com/rancher/elemental-cli/internal/version"
	"github.com/rancher/elemental-cli/pkg/config"
	"github.com/rancher/elemental-cli/pkg/constants"
	"github.com/rancher/elemental-cli/pkg/luet"
	v1 "github.com/rancher/elemental-cli/pkg/types/v1"
	"github.com/rancher/elemental-cli/pkg/utils"
)

var decodeHook = viper.DecodeHook(
	mapstructure.ComposeDecodeHookFunc(
		UnmarshalerHook(),
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	),
)

type Unmarshaler interface {
	CustomUnmarshal(interface{}) (bool, error)
}

func UnmarshalerHook() mapstructure.DecodeHookFunc {
	return func(from reflect.Value, to reflect.Value) (interface{}, error) {
		// get the destination object address if it is not passed by reference
		if to.CanAddr() {
			to = to.Addr()
		}
		// If the destination implements the unmarshaling interface
		u, ok := to.Interface().(Unmarshaler)
		if !ok {
			return from.Interface(), nil
		}
		// If it is nil and a pointer, create and assign the target value first
		if to.IsNil() && to.Type().Kind() == reflect.Ptr {
			to.Set(reflect.New(to.Type().Elem()))
			u = to.Interface().(Unmarshaler)
		}
		// Call the custom unmarshaling method
		cont, err := u.CustomUnmarshal(from.Interface())
		if cont {
			// Continue with the decoding stack
			return from.Interface(), err
		}
		// Decoding finalized
		return to.Interface(), err
	}
}

// setDecoder sets ZeroFields mastructure attribute to true
func setDecoder(config *mapstructure.DecoderConfig) {
	// Make sure we zero fields before applying them, this is relevant for slices
	// so we do not merge with any already present value and directly apply whatever
	// we got form configs.
	config.ZeroFields = true
}

// BindGivenFlags binds to viper only passed flags, ignoring any non provided flag
func bindGivenFlags(vp *viper.Viper, flagSet *pflag.FlagSet) {
	if flagSet != nil {
		flagSet.VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				_ = vp.BindPFlag(f.Name, f)
			}
		})
	}
}

func ReadConfigBuild(configDir string, flags *pflag.FlagSet, mounter mount.Interface) (*v1.BuildConfig, error) {
	logger := v1.NewLogger()
	if configDir == "" {
		configDir = "."
	}

	l := luet.NewLuet(luet.WithLogger(logger))

	cfg := config.NewBuildConfig(
		config.WithLogger(logger),
		config.WithMounter(mounter),
		config.WithLuet(l),
	)

	configLogger(cfg.Logger, cfg.Fs)

	viper.AddConfigPath(configDir)
	viper.SetConfigType("yaml")
	viper.SetConfigName("manifest.yaml")
	// If a config file is found, read it in.
	_ = viper.MergeInConfig()

	// Bind buildconfig flags
	bindGivenFlags(viper.GetViper(), flags)
	// merge environment variables on top for rootCmd
	viperReadEnv(viper.GetViper(), "BUILD", constants.GetBuildKeyEnvMap())

	// unmarshal all the vars into the config object
	err := viper.Unmarshal(cfg, setDecoder, decodeHook)
	if err != nil {
		cfg.Logger.Warnf("error unmarshalling config: %s", err)
	}

	err = cfg.Sanitize()
	cfg.Logger.Debugf("Full config loaded: %s", litter.Sdump(cfg))
	return cfg, err
}

func ReadConfigRun(configDir string, flags *pflag.FlagSet, mounter mount.Interface) (*v1.RunConfig, error) {
	cfg := config.NewRunConfig(
		config.WithLogger(v1.NewLogger()),
		config.WithMounter(mounter),
	)

	configLogger(cfg.Logger, cfg.Fs)

	// TODO: is this really needed? It feels quite wrong, shouldn't it be loaded
	// as regular environment variables?
	// IMHO loading os-release as env variables should be sufficient here
	cfgDefault := []string{"/etc/os-release"}
	for _, c := range cfgDefault {
		if exists, _ := utils.Exists(cfg.Fs, c); exists {
			viper.SetConfigFile(c)
			viper.SetConfigType("env")
			cobra.CheckErr(viper.MergeInConfig())
		}
	}

	// merge yaml config files on top of default runconfig
	if exists, _ := utils.Exists(cfg.Fs, configDir); exists {
		viper.AddConfigPath(configDir)
		viper.SetConfigType("yaml")
		viper.SetConfigName("config")
		// If a config file is found, read it in.
		err := viper.MergeInConfig()
		if err != nil {
			cfg.Logger.Warnf("error merging config files: %s", err)
		}
	}

	// Load extra config files on configdir/config.d/ so we can override config values
	cfgExtra := fmt.Sprintf("%s/config.d/", strings.TrimSuffix(configDir, "/"))
	if exists, _ := utils.Exists(cfg.Fs, cfgExtra); exists {
		viper.AddConfigPath(cfgExtra)
		_ = filepath.WalkDir(cfgExtra, func(path string, d fs.DirEntry, err error) error {
			if !d.IsDir() && filepath.Ext(d.Name()) == ".yaml" {
				viper.SetConfigType("yaml")
				viper.SetConfigName(strings.TrimSuffix(d.Name(), ".yaml"))
				cobra.CheckErr(viper.MergeInConfig())
			}
			return nil
		})
	}

	// Bind runconfig flags
	bindGivenFlags(viper.GetViper(), flags)
	// merge environment variables on top for rootCmd
	viperReadEnv(viper.GetViper(), "", constants.GetRunKeyEnvMap())

	// unmarshal all the vars into the RunConfig object
	err := viper.Unmarshal(cfg, setDecoder, decodeHook)
	if err != nil {
		cfg.Logger.Warnf("error unmarshalling RunConfig: %s", err)
	}

	err = cfg.Sanitize()
	cfg.Logger.Debugf("Full config loaded: %s", litter.Sdump(cfg))
	return cfg, err
}

func ReadInstallSpec(r *v1.RunConfig, flags *pflag.FlagSet) (*v1.InstallSpec, error) {
	install := config.NewInstallSpec(r.Config)
	vp := viper.Sub("install")
	if vp == nil {
		vp = viper.New()
	}
	// Bind install cmd flags
	bindGivenFlags(vp, flags)
	// Bind install env vars
	viperReadEnv(vp, "INSTALL", constants.GetInstallKeyEnvMap())

	err := vp.Unmarshal(install, setDecoder, decodeHook)
	if err != nil {
		r.Logger.Warnf("error unmarshalling InstallSpec: %s", err)
	}
	err = install.Sanitize()
	r.Logger.Debugf("Loaded install spec: %s", litter.Sdump(install))
	return install, err
}

func ReadResetSpec(r *v1.RunConfig, flags *pflag.FlagSet) (*v1.ResetSpec, error) {
	reset, err := config.NewResetSpec(r.Config)
	if err != nil {
		return nil, fmt.Errorf("failed initializing reset spec: %v", err)
	}
	vp := viper.Sub("reset")
	if vp == nil {
		vp = viper.New()
	}
	// Bind reset cmd flags
	bindGivenFlags(vp, flags)
	// Bind reset env vars
	viperReadEnv(vp, "RESET", constants.GetResetKeyEnvMap())

	err = vp.Unmarshal(reset, setDecoder, decodeHook)
	if err != nil {
		r.Logger.Warnf("error unmarshalling ResetSpec: %s", err)
	}
	err = reset.Sanitize()
	r.Logger.Debugf("Loaded reset spec: %s", litter.Sdump(reset))
	return reset, err
}

func ReadUpgradeSpec(r *v1.RunConfig, flags *pflag.FlagSet) (*v1.UpgradeSpec, error) {
	upgrade, err := config.NewUpgradeSpec(r.Config)
	if err != nil {
		return nil, fmt.Errorf("failed initializing upgrade spec: %v", err)
	}
	vp := viper.Sub("upgrade")
	if vp == nil {
		vp = viper.New()
	}
	// Bind upgrade cmd flags
	bindGivenFlags(vp, flags)
	// Bind upgrade env vars
	viperReadEnv(vp, "UPGRADE", constants.GetUpgradeKeyEnvMap())

	err = vp.Unmarshal(upgrade, setDecoder, decodeHook)
	if err != nil {
		r.Logger.Warnf("error unmarshalling UpgradeSpec: %s", err)
	}
	err = upgrade.Sanitize()
	r.Logger.Debugf("Loaded upgrade UpgradeSpec: %s", litter.Sdump(upgrade))
	return upgrade, err
}

func ReadBuildISO(b *v1.BuildConfig, flags *pflag.FlagSet) (*v1.LiveISO, error) {
	iso := config.NewISO()
	vp := viper.Sub("iso")
	if vp == nil {
		vp = viper.New()
	}
	// Bind build-iso cmd flags
	bindGivenFlags(vp, flags)
	// Bind build-iso env vars
	viperReadEnv(vp, "ISO", constants.GetISOKeyEnvMap())

	err := vp.Unmarshal(iso, setDecoder, decodeHook)
	if err != nil {
		b.Logger.Warnf("error unmarshalling LiveISO: %s", err)
	}
	err = iso.Sanitize()
	b.Logger.Debugf("Loaded LiveISO: %s", litter.Sdump(iso))
	return iso, err
}

func ReadBuildPXE(b *v1.BuildConfig, flags *pflag.FlagSet) (*v1.PXEConf, error) {
	pxe := config.NewPXE()
	vp := viper.Sub("pxe")
	if vp == nil {
		vp = viper.New()
	}
	// Bind build-pxe cmd flags
	bindGivenFlags(vp, flags)
	// Bind build-pxe env vars
	viperReadEnv(vp, "PXE", constants.GetISOKeyEnvMap())

	err := vp.Unmarshal(pxe, setDecoder, decodeHook)
	if err != nil {
		b.Logger.Warnf("error unmarshalling LiveISO: %s", err)
	}

	b.Logger.Debugf("Loaded PXEConf: %s", litter.Sdump(pxe))
	return pxe, err
}

func ReadBuildDisk(b *v1.BuildConfig, flags *pflag.FlagSet) (*v1.RawDisk, error) {
	disk := config.NewRawDisk()
	vp := viper.Sub("raw_disk")
	if vp == nil {
		vp = viper.New()
	}
	// Bind build-disk cmd flags
	bindGivenFlags(vp, flags)
	// Bind build-disk env vars
	viperReadEnv(vp, "RAWDISK", constants.GetDiskKeyEnvMap())

	err := vp.Unmarshal(disk, setDecoder, decodeHook)
	if err != nil {
		b.Logger.Warnf("error unmarshalling RawDisk: %s", err)
	}
	err = disk.Sanitize()
	b.Logger.Debugf("Loaded RawDisk: %s", litter.Sdump(disk))
	return disk, err
}

func configLogger(log v1.Logger, vfs v1.FS) {
	// Set debug level
	if viper.GetBool("debug") {
		log.SetLevel(v1.DebugLevel())
	}

	// Set formatter so both file and stdout format are equal
	log.SetFormatter(&logrus.TextFormatter{
		ForceColors:      true,
		DisableColors:    false,
		DisableTimestamp: false,
		FullTimestamp:    true,
	})

	// Logfile
	logfile := viper.GetString("logfile")
	if logfile != "" {
		o, err := vfs.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fs.ModePerm)

		if err != nil {
			log.Errorf("Could not open %s for logging to file: %s", logfile, err.Error())
		}

		if viper.GetBool("quiet") { // if quiet is set, only set the log to the file
			log.SetOutput(o)
		} else { // else set it to both stdout and the file
			mw := io.MultiWriter(os.Stdout, o)
			log.SetOutput(mw)
		}
	} else { // no logfile
		if viper.GetBool("quiet") { // quiet is enabled so discard all logging
			log.SetOutput(ioutil.Discard)
		} else { // default to stdout
			log.SetOutput(os.Stdout)
		}
	}

	v := version.Get()
	if log.GetLevel() == logrus.DebugLevel {
		log.Debugf("Starting elemental version %s on commit %s", v.Version, v.GitCommit)
	} else {
		log.Infof("Starting elemental version %s", v.Version)
	}
}

func viperReadEnv(vp *viper.Viper, prefix string, keyMap map[string]string) {
	// If we expect to override complex keys in the config, i.e. configs
	// that are nested, we probably need to manually do the env stuff
	// ourselves, as this will only match keys in the config root
	replacer := strings.NewReplacer("-", "_")
	vp.SetEnvKeyReplacer(replacer)

	if prefix == "" {
		prefix = "ELEMENTAL"
	} else {
		prefix = fmt.Sprintf("ELEMENTAL_%s", prefix)
	}

	// Manually bind keys to env variable if custom names are needed.
	for k, v := range keyMap {
		_ = vp.BindEnv(k, fmt.Sprintf("%s_%s", prefix, v))
	}
}
