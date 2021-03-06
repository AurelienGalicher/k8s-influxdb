/*
Copyright 2014 The Kubernetes Authors All rights reserved.

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

package clientcmd

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/imdario/mergo"
	"revision.aeip.apigee.net/spatel/k8s-influxdb/influxdb/Godeps/_workspace/src/github.com/ghodss/yaml"
	"revision.aeip.apigee.net/spatel/k8s-influxdb/influxdb/Godeps/_workspace/src/github.com/golang/glog"

	clientcmdapi "revision.aeip.apigee.net/spatel/k8s-influxdb/influxdb/Godeps/_workspace/src/k8s.io/kubernetes/pkg/client/unversioned/clientcmd/api"
	clientcmdlatest "revision.aeip.apigee.net/spatel/k8s-influxdb/influxdb/Godeps/_workspace/src/k8s.io/kubernetes/pkg/client/unversioned/clientcmd/api/latest"
	utilerrors "revision.aeip.apigee.net/spatel/k8s-influxdb/influxdb/Godeps/_workspace/src/k8s.io/kubernetes/pkg/util/errors"
)

const (
	RecommendedConfigPathFlag   = "kubeconfig"
	RecommendedConfigPathEnvVar = "KUBECONFIG"
	RecommendedHomeDir          = ".kube"
	RecommendedFileName         = "config"
	RecommendedSchemaName       = "schema"
)

var OldRecommendedHomeFile = path.Join(os.Getenv("HOME"), "/.kube/.kubeconfig")
var RecommendedHomeFile = path.Join(os.Getenv("HOME"), RecommendedHomeDir, RecommendedFileName)
var RecommendedSchemaFile = path.Join(os.Getenv("HOME"), RecommendedHomeDir, RecommendedSchemaName)

// ClientConfigLoadingRules is an ExplicitPath and string slice of specific locations that are used for merging together a Config
// Callers can put the chain together however they want, but we'd recommend:
// EnvVarPathFiles if set (a list of files if set) OR the HomeDirectoryPath
// ExplicitPath is special, because if a user specifically requests a certain file be used and error is reported if thie file is not present
type ClientConfigLoadingRules struct {
	ExplicitPath string
	Precedence   []string

	// MigrationRules is a map of destination files to source files.  If a destination file is not present, then the source file is checked.
	// If the source file is present, then it is copied to the destination file BEFORE any further loading happens.
	MigrationRules map[string]string

	// DoNotResolvePaths indicates whether or not to resolve paths with respect to the originating files.  This is phrased as a negative so
	// that a default object that doesn't set this will usually get the behavior it wants.
	DoNotResolvePaths bool
}

// NewDefaultClientConfigLoadingRules returns a ClientConfigLoadingRules object with default fields filled in.  You are not required to
// use this constructor
func NewDefaultClientConfigLoadingRules() *ClientConfigLoadingRules {
	chain := []string{}
	migrationRules := map[string]string{}

	envVarFiles := os.Getenv(RecommendedConfigPathEnvVar)
	if len(envVarFiles) != 0 {
		chain = append(chain, filepath.SplitList(envVarFiles)...)

	} else {
		chain = append(chain, RecommendedHomeFile)
		migrationRules[RecommendedHomeFile] = OldRecommendedHomeFile

	}

	return &ClientConfigLoadingRules{
		Precedence:     chain,
		MigrationRules: migrationRules,
	}
}

// Load starts by running the MigrationRules and then
// takes the loading rules and returns a Config object based on following rules.
//   if the ExplicitPath, return the unmerged explicit file
//   Otherwise, return a merged config based on the Precedence slice
// A missing ExplicitPath file produces an error. Empty filenames or other missing files are ignored.
// Read errors or files with non-deserializable content produce errors.
// The first file to set a particular map key wins and map key's value is never changed.
// BUT, if you set a struct value that is NOT contained inside of map, the value WILL be changed.
// This results in some odd looking logic to merge in one direction, merge in the other, and then merge the two.
// It also means that if two files specify a "red-user", only values from the first file's red-user are used.  Even
// non-conflicting entries from the second file's "red-user" are discarded.
// Relative paths inside of the .kubeconfig files are resolved against the .kubeconfig file's parent folder
// and only absolute file paths are returned.
func (rules *ClientConfigLoadingRules) Load() (*clientcmdapi.Config, error) {
	if err := rules.Migrate(); err != nil {
		return nil, err
	}

	errlist := []error{}

	kubeConfigFiles := []string{}

	// Make sure a file we were explicitly told to use exists
	if len(rules.ExplicitPath) > 0 {
		if _, err := os.Stat(rules.ExplicitPath); os.IsNotExist(err) {
			return nil, err
		}
		kubeConfigFiles = append(kubeConfigFiles, rules.ExplicitPath)

	} else {
		kubeConfigFiles = append(kubeConfigFiles, rules.Precedence...)

	}

	// first merge all of our maps
	mapConfig := clientcmdapi.NewConfig()
	for _, file := range kubeConfigFiles {
		if err := mergeConfigWithFile(mapConfig, file); err != nil {
			errlist = append(errlist, err)
		}
	}

	// merge all of the struct values in the reverse order so that priority is given correctly
	// errors are not added to the list the second time
	nonMapConfig := clientcmdapi.NewConfig()
	for i := len(kubeConfigFiles) - 1; i >= 0; i-- {
		file := kubeConfigFiles[i]
		mergeConfigWithFile(nonMapConfig, file)
	}

	// since values are overwritten, but maps values are not, we can merge the non-map config on top of the map config and
	// get the values we expect.
	config := clientcmdapi.NewConfig()
	mergo.Merge(config, mapConfig)
	mergo.Merge(config, nonMapConfig)

	if rules.ResolvePaths() {
		if err := ResolveLocalPaths(config); err != nil {
			errlist = append(errlist, err)
		}
	}

	return config, utilerrors.NewAggregate(errlist)
}

// Migrate uses the MigrationRules map.  If a destination file is not present, then the source file is checked.
// If the source file is present, then it is copied to the destination file BEFORE any further loading happens.
func (rules *ClientConfigLoadingRules) Migrate() error {
	if rules.MigrationRules == nil {
		return nil
	}

	for destination, source := range rules.MigrationRules {
		if _, err := os.Stat(destination); err == nil {
			// if the destination already exists, do nothing
			continue
		} else if !os.IsNotExist(err) {
			// if we had an error other than non-existence, fail
			return err
		}

		if sourceInfo, err := os.Stat(source); err != nil {
			if os.IsNotExist(err) {
				// if the source file doesn't exist, there's no work to do.
				continue
			}

			// if we had an error other than non-existence, fail
			return err
		} else if sourceInfo.IsDir() {
			return fmt.Errorf("cannot migrate %v to %v because it is a directory", source, destination)
		}

		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(destination)
		if err != nil {
			return err
		}
		defer out.Close()

		if _, err = io.Copy(out, in); err != nil {
			return err
		}
	}

	return nil
}

func mergeConfigWithFile(startingConfig *clientcmdapi.Config, filename string) error {
	if len(filename) == 0 {
		// no work to do
		return nil
	}

	config, err := LoadFromFile(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("Error loading config file \"%s\": %v", filename, err)
	}

	mergo.Merge(startingConfig, config)

	return nil
}

// LoadFromFile takes a filename and deserializes the contents into Config object
func LoadFromFile(filename string) (*clientcmdapi.Config, error) {
	kubeconfigBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	config, err := Load(kubeconfigBytes)
	if err != nil {
		return nil, err
	}
	glog.V(3).Infoln("Config loaded from file", filename)

	// set LocationOfOrigin on every Cluster, User, and Context
	for key, obj := range config.AuthInfos {
		obj.LocationOfOrigin = filename
		config.AuthInfos[key] = obj
	}
	for key, obj := range config.Clusters {
		obj.LocationOfOrigin = filename
		config.Clusters[key] = obj
	}
	for key, obj := range config.Contexts {
		obj.LocationOfOrigin = filename
		config.Contexts[key] = obj
	}

	if config.AuthInfos == nil {
		config.AuthInfos = map[string]*clientcmdapi.AuthInfo{}
	}
	if config.Clusters == nil {
		config.Clusters = map[string]*clientcmdapi.Cluster{}
	}
	if config.Contexts == nil {
		config.Contexts = map[string]*clientcmdapi.Context{}
	}

	return config, nil
}

// Load takes a byte slice and deserializes the contents into Config object.
// Encapsulates deserialization without assuming the source is a file.
func Load(data []byte) (*clientcmdapi.Config, error) {
	config := clientcmdapi.NewConfig()
	// if there's no data in a file, return the default object instead of failing (DecodeInto reject empty input)
	if len(data) == 0 {
		return config, nil
	}

	if err := clientcmdlatest.Codec.DecodeInto(data, config); err != nil {
		return nil, err
	}
	return config, nil
}

// WriteToFile serializes the config to yaml and writes it out to a file.  If not present, it creates the file with the mode 0600.  If it is present
// it stomps the contents
func WriteToFile(config clientcmdapi.Config, filename string) error {
	content, err := Write(config)
	if err != nil {
		return err
	}
	dir := filepath.Dir(filename)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err = os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	if err := ioutil.WriteFile(filename, content, 0600); err != nil {
		return err
	}
	return nil
}

// Write serializes the config to yaml.
// Encapsulates serialization without assuming the destination is a file.
func Write(config clientcmdapi.Config) ([]byte, error) {
	json, err := clientcmdlatest.Codec.Encode(&config)
	if err != nil {
		return nil, err
	}
	content, err := yaml.JSONToYAML(json)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func (rules ClientConfigLoadingRules) ResolvePaths() bool {
	return !rules.DoNotResolvePaths
}

// ResolveLocalPaths resolves all relative paths in the config object with respect to the stanza's LocationOfOrigin
// this cannot be done directly inside of LoadFromFile because doing so there would make it impossible to load a file without
// modification of its contents.
func ResolveLocalPaths(config *clientcmdapi.Config) error {
	for _, cluster := range config.Clusters {
		if len(cluster.LocationOfOrigin) == 0 {
			continue
		}
		base, err := filepath.Abs(filepath.Dir(cluster.LocationOfOrigin))
		if err != nil {
			return fmt.Errorf("Could not determine the absolute path of config file %s: %v", cluster.LocationOfOrigin, err)
		}

		if err := ResolvePaths(GetClusterFileReferences(cluster), base); err != nil {
			return err
		}
	}
	for _, authInfo := range config.AuthInfos {
		if len(authInfo.LocationOfOrigin) == 0 {
			continue
		}
		base, err := filepath.Abs(filepath.Dir(authInfo.LocationOfOrigin))
		if err != nil {
			return fmt.Errorf("Could not determine the absolute path of config file %s: %v", authInfo.LocationOfOrigin, err)
		}

		if err := ResolvePaths(GetAuthInfoFileReferences(authInfo), base); err != nil {
			return err
		}
	}

	return nil
}

// RelativizeClusterLocalPaths first absolutizes the paths by calling ResolveLocalPaths.  This assumes that any NEW path is already
// absolute, but any existing path will be resolved relative to LocationOfOrigin
func RelativizeClusterLocalPaths(cluster *clientcmdapi.Cluster) error {
	if len(cluster.LocationOfOrigin) == 0 {
		return fmt.Errorf("no location of origin for %s", cluster.Server)
	}
	base, err := filepath.Abs(filepath.Dir(cluster.LocationOfOrigin))
	if err != nil {
		return fmt.Errorf("could not determine the absolute path of config file %s: %v", cluster.LocationOfOrigin, err)
	}

	if err := ResolvePaths(GetClusterFileReferences(cluster), base); err != nil {
		return err
	}
	if err := RelativizePathWithNoBacksteps(GetClusterFileReferences(cluster), base); err != nil {
		return err
	}

	return nil
}

// RelativizeAuthInfoLocalPaths first absolutizes the paths by calling ResolveLocalPaths.  This assumes that any NEW path is already
// absolute, but any existing path will be resolved relative to LocationOfOrigin
func RelativizeAuthInfoLocalPaths(authInfo *clientcmdapi.AuthInfo) error {
	if len(authInfo.LocationOfOrigin) == 0 {
		return fmt.Errorf("no location of origin for %v", authInfo)
	}
	base, err := filepath.Abs(filepath.Dir(authInfo.LocationOfOrigin))
	if err != nil {
		return fmt.Errorf("could not determine the absolute path of config file %s: %v", authInfo.LocationOfOrigin, err)
	}

	if err := ResolvePaths(GetAuthInfoFileReferences(authInfo), base); err != nil {
		return err
	}
	if err := RelativizePathWithNoBacksteps(GetAuthInfoFileReferences(authInfo), base); err != nil {
		return err
	}

	return nil
}

func RelativizeConfigPaths(config *clientcmdapi.Config, base string) error {
	return RelativizePathWithNoBacksteps(GetConfigFileReferences(config), base)
}

func ResolveConfigPaths(config *clientcmdapi.Config, base string) error {
	return ResolvePaths(GetConfigFileReferences(config), base)
}

func GetConfigFileReferences(config *clientcmdapi.Config) []*string {
	refs := []*string{}

	for _, cluster := range config.Clusters {
		refs = append(refs, GetClusterFileReferences(cluster)...)
	}
	for _, authInfo := range config.AuthInfos {
		refs = append(refs, GetAuthInfoFileReferences(authInfo)...)
	}

	return refs
}

func GetClusterFileReferences(cluster *clientcmdapi.Cluster) []*string {
	return []*string{&cluster.CertificateAuthority}
}

func GetAuthInfoFileReferences(authInfo *clientcmdapi.AuthInfo) []*string {
	return []*string{&authInfo.ClientCertificate, &authInfo.ClientKey}
}

// ResolvePaths updates the given refs to be absolute paths, relative to the given base directory
func ResolvePaths(refs []*string, base string) error {
	for _, ref := range refs {
		// Don't resolve empty paths
		if len(*ref) > 0 {
			// Don't resolve absolute paths
			if !filepath.IsAbs(*ref) {
				*ref = filepath.Join(base, *ref)
			}
		}
	}
	return nil
}

// RelativizePathWithNoBacksteps updates the given refs to be relative paths, relative to the given base directory as long as they do not require backsteps.
// Any path requiring a backstep is left as-is as long it is absolute.  Any non-absolute path that can't be relativized produces an error
func RelativizePathWithNoBacksteps(refs []*string, base string) error {
	for _, ref := range refs {
		// Don't relativize empty paths
		if len(*ref) > 0 {
			rel, err := MakeRelative(*ref, base)
			if err != nil {
				return err
			}

			// if we have a backstep, don't mess with the path
			if strings.HasPrefix(rel, "../") {
				if filepath.IsAbs(*ref) {
					continue
				}

				return fmt.Errorf("%v requires backsteps and is not absolute", *ref)
			}

			*ref = rel
		}
	}
	return nil
}

func MakeRelative(path, base string) (string, error) {
	if len(path) > 0 {
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return path, err
		}
		return rel, nil
	}
	return path, nil
}
