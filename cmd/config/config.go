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
	"github.com/rancher-sandbox/elemental/internal/version"
	"github.com/rancher-sandbox/elemental/pkg/config"
	"github.com/rancher-sandbox/elemental/pkg/luet"
	v1 "github.com/rancher-sandbox/elemental/pkg/types/v1"
	"github.com/rancher-sandbox/elemental/pkg/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"k8s.io/mount-utils"
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
func bindGivenFlags(cmd *cobra.Command) {
	set := cmd.Flags()
	set.VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			_ = viper.BindPFlag(f.Name, f)
		}
	})
}

func ReadConfigBuild(configDir string, mounter mount.Interface) (*v1.BuildConfig, error) {
	logger := v1.NewLogger()
	arch := viper.GetString("arch")
	if arch == "" {
		arch = "x86_64"
	}

	cfg := config.NewBuildConfig(
		config.WithLogger(logger),
		config.WithMounter(mounter),
		config.WithLuet(luet.NewLuet(luet.WithLogger(logger))),
		config.WithArch(arch),
	)

	configLogger(cfg.Logger, cfg.Fs)

	viper.AddConfigPath(configDir)
	viper.SetConfigType("yaml")
	viper.SetConfigName("manifest.yaml")
	// If a config file is found, read it in.
	_ = viper.MergeInConfig()
	viperReadEnv()

	// unmarshal all the vars into the config object
	err := viper.Unmarshal(cfg, setDecoder)
	if err != nil {
		cfg.Logger.Warnf("error unmarshalling config: %s", err)
	}

	cfg.Logger.Debugf("Full config loaded: %+v", cfg)

	return cfg, nil
}

func ReadConfigRun(configDir string, mounter mount.Interface) (*v1.RunConfig, error) {
	cfg := config.NewRunConfig(
		config.WithLogger(v1.NewLogger()),
		config.WithMounter(mounter),
	)

	configLogger(cfg.Logger, cfg.Fs)

	cfgDefault := []string{"/etc/os-release", "/etc/cos/config", "/etc/cos-upgrade-image"}

	for _, c := range cfgDefault {
		if _, err := os.Stat(c); err == nil {
			viper.SetConfigFile(c)
			viper.SetConfigType("env")
			cobra.CheckErr(viper.MergeInConfig())
		}
	}

	if configDir != "" {
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
		if _, err := os.Stat(cfgExtra); err == nil {
			viper.AddConfigPath(cfgExtra)
			_ = filepath.WalkDir(cfgExtra, func(path string, d fs.DirEntry, err error) error {
				if !d.IsDir() {
					viper.SetConfigType("yaml")
					viper.SetConfigName(d.Name())
					cobra.CheckErr(viper.MergeInConfig())
				}
				return nil
			})
		}
	}

	viperReadEnv()

	// unmarshal all the vars into the config object
	err := viper.Unmarshal(cfg, setDecoder)
	if err != nil {
		cfg.Logger.Warnf("error unmarshalling config: %s", err)
	}

	cfg.Logger.Debugf("Full config loaded: %+v", cfg)

	return cfg, nil
}

func ReadConfigRunNew(configDir string, cmd *cobra.Command, mounter mount.Interface) (*v1.RunConfigNew, error) {
	cfg := config.NewRunConfigNew(
		config.WithLogger(v1.NewLogger()),
		config.WithMounter(mounter),
	)

	configLogger(cfg.Logger, cfg.Fs)

	cfgDefault := []string{"/etc/os-release", "/etc/cos/config", "/etc/cos-upgrade-image"}

	// First, read env config files
	for _, c := range cfgDefault {
		if _, err := os.Stat(c); err == nil {
			viper.SetConfigFile(c)
			viper.SetConfigType("env")
			cobra.CheckErr(viper.MergeInConfig())
		}
	}

	// Second, merge yaml config files on top
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
	if _, err := os.Stat(cfgExtra); err == nil {
		viper.AddConfigPath(cfgExtra)
		_ = filepath.WalkDir(cfgExtra, func(path string, d fs.DirEntry, err error) error {
			if !d.IsDir() {
				viper.SetConfigName(d.Name())
				cobra.CheckErr(viper.MergeInConfig())
			}
			return nil
		})
	}

	// Third, merge command client flags on top
	bindGivenFlags(cmd)

	// Finally, merge environment variables on top
	viperReadEnv()

	// unmarshal all the vars into the config object
	err := viper.Unmarshal(cfg, setDecoder, decodeHook)
	if err != nil {
		cfg.Logger.Warnf("error unmarshalling config: %s", err)
	}

	cfg.Logger.Debugf("Full config loaded: %+v", cfg)

	return cfg, nil
}

/*func newMetaDecoder(result interface{}) (*mapstructure.Decoder, error) {
	decoConfig := &mapstructure.DecoderConfig{}
	decoConfig.DecodeHook = mapstructure.ComposeDecodeHookFunc(
		UnmarshalerHook(),
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	)
	decoConfig.ZeroFields = true
	decoConfig.Result = result
	deco, err := mapstructure.NewDecoder(decoConfig)
	return deco, err
}

func readNestedRunConfigSpec(r *v1.RunConfigNew, nestedSpec interface{}, nestedTag string) error {
	data, ok := r.Meta[nestedTag]
	if !ok {
		// nothing to decode
		return nil
	}

	return decodeMapData(data, nestedSpec)
}

func decodeMapData(data interface{}, result interface{}) error {
	deco, err := newMetaDecoder(result)
	if err != nil {
		return fmt.Errorf("could not initialize decoder: %v", err)
	}
	err = deco.Decode(data)
	return err
}*/
func viperRemapKeys(vp *viper.Viper, keyRemap map[string]string) {
	for formerKey, newKey := range keyRemap {
		v := viper.Get(formerKey)
		if v == nil {
			continue
		}
		vp.Set(newKey, v)
	}
}

func ReadInstallSpec(r *v1.RunConfigNew, keyRemap map[string]string) (*v1.InstallSpec, error) {
	install := config.NewInstallSpec(r.Config)
	vp := viper.Sub("install")
	if vp == nil {
		vp = viper.New()
	}
	viperRemapKeys(vp, keyRemap)
	err := vp.Unmarshal(install, setDecoder, decodeHook)
	r.Logger.Debugf("Loaded install spec: %+v", install)
	return install, err
}

func ReadResetSpec(r *v1.RunConfigNew, keyRemap map[string]string) (*v1.ResetSpec, error) {
	reset, err := config.NewResetSpec(r.Config)
	if err != nil {
		return nil, fmt.Errorf("failed initializing reset spec: %v", err)
	}
	vp := viper.Sub("reset")
	if vp == nil {
		vp = viper.New()
	}
	viperRemapKeys(vp, keyRemap)
	err = vp.Unmarshal(reset, setDecoder, decodeHook)
	r.Logger.Debugf("Loaded reset spec: %+v", reset)
	return reset, err
}

func ReadUpgradeSpec(r *v1.RunConfigNew, keyRemap map[string]string) (*v1.UpgradeSpec, error) {
	upgrade, err := config.NewUpgradeSpec(r.Config)
	if err != nil {
		return nil, fmt.Errorf("failed initializing upgrade spec: %v", err)
	}
	vp := viper.Sub("upgrade")
	if vp == nil {
		vp = viper.New()
	}
	viperRemapKeys(vp, keyRemap)
	err = vp.Unmarshal(upgrade, setDecoder, decodeHook)
	r.Logger.Debugf("Loaded upgrade spec: %+v", upgrade)
	return upgrade, err
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
	log.Infof("Starting elemental version %s", v.Version)
}

func viperReadEnv() {
	// Set the prefix for vars so we get only the ones starting with ELEMENTAL
	viper.SetEnvPrefix("ELEMENTAL")

	// If we expect to override complex keys in the config, i.e. configs that are nested, we probably need to manually do
	// the env stuff ourselves, as this will only match keys in the config root
	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)

	// Manually bind public key env variable as it uses a different name in config files or flags.
	_ = viper.BindEnv("CosingPubKey", "COSIGN_PUBLIC_KEY_LOCATION")

	viper.AutomaticEnv() // read in environment variables that match
}
