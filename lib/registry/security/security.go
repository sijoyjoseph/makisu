//  Copyright (c) 2018 Uber Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package security

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net/http"
	"path"

	"github.com/uber/makisu/lib/pathutils"
	"github.com/uber/makisu/lib/utils"
	"github.com/uber/makisu/lib/utils/httputil"

	"github.com/docker/docker-credential-helpers/client"
	"github.com/docker/engine-api/types"
)

const tokenUsername = "<token>"

var credentialHelperPrefix = path.Join(pathutils.DefaultInternalDir, "docker-credential-")

// BasicAuthConfig is a simple wrapper of Docker's types.AuthConfig with addtional support
// for a password file.
type BasicAuthConfig struct {
	types.AuthConfig `yaml:",inline"`
	PasswordFile     string `yaml:"password_file" json:"password_file"`
}

// Get returns an AuthConfig.
func (c *BasicAuthConfig) Get() (types.AuthConfig, error) {
	if c.PasswordFile != "" {
		password, err := ioutil.ReadFile(c.PasswordFile)
		if err != nil {
			return types.AuthConfig{}, fmt.Errorf("read password file: %s", err)
		}
		c.AuthConfig.Password = string(password)
	}
	return c.AuthConfig, nil
}

// Config contains tls and basic auth configuration.
type Config struct {
	TLS                    *httputil.TLSConfig `yaml:"tls" json:"tls"`
	BasicAuth              *BasicAuthConfig    `yaml:"basic" json:"basic"`
	RemoteCredentialsStore string              `yaml:"credsStore" json:"credsStore"`
}

// ApplyDefaults applies default configuration.
func (c Config) ApplyDefaults() Config {
	if c.TLS == nil {
		c.TLS = &httputil.TLSConfig{}
	}
	if c.TLS.CA.Cert.Path == "" {
		c.TLS.CA.Cert.Path = utils.DefaultEnv("SSL_CERT_DIR", pathutils.DefaultCACertsPath)
	}
	return c
}

// GetHTTPOption returns httputil.Option based on the security configuration.
func (c Config) GetHTTPOption(addr, repo string) (httputil.SendOption, error) {
	shouldUseBasicAuth := (c.BasicAuth != nil || c.RemoteCredentialsStore != "")

	var tlsClientConfig *tls.Config
	var err error
	if c.TLS != nil {
		tlsClientConfig, err = c.TLS.BuildClient()
		if err != nil {
			return nil, fmt.Errorf("build tls config: %s", err)
		}
		if !shouldUseBasicAuth {
			return httputil.SendTLS(tlsClientConfig), nil
		}
	}

	if shouldUseBasicAuth {
		authConfig, err := c.getCredentials(c.RemoteCredentialsStore, addr)
		if err != nil {
			return nil, fmt.Errorf("get credentials: %s", err)
		}
		tr := http.DefaultTransport.(*http.Transport)
		tr.TLSClientConfig = tlsClientConfig // If tlsClientConfig is nil, default is used.
		rt, err := BasicAuthTransport(addr, repo, tr, authConfig)
		if err != nil {
			return nil, fmt.Errorf("basic auth: %s", err)
		}
		return httputil.SendTLSTransport(rt), nil
	}
	return httputil.SendNoop(), nil
}

func (c Config) getCredentials(helper, addr string) (types.AuthConfig, error) {
	var authConfig types.AuthConfig
	var err error
	if c.BasicAuth != nil {
		authConfig, err = c.BasicAuth.Get()
		if err != nil {
			return types.AuthConfig{}, fmt.Errorf("get basic auth config: %s", err)
		}
	}
	if helper != "" {
		authConfig, err = c.getCredentialFromHelper(helper, addr)
		if err != nil {
			return types.AuthConfig{}, fmt.Errorf("get credentials from helper %s: %s", helper, err)
		}
	}
	return authConfig, nil
}

func (c Config) getCredentialFromHelper(helper, addr string) (types.AuthConfig, error) {
	helperFullName := credentialHelperPrefix + helper
	creds, err := client.Get(client.NewShellProgramFunc(helperFullName), addr)
	if err != nil {
		return types.AuthConfig{}, err
	}

	var ret types.AuthConfig
	if c.BasicAuth != nil {
		ret, err = c.BasicAuth.Get()
		if err != nil {
			return types.AuthConfig{}, fmt.Errorf("get basic auth config: %s", err)
		}
	}
	ret.ServerAddress = addr
	if creds.Username == tokenUsername {
		ret.IdentityToken = creds.Secret
	} else {
		ret.Password = creds.Secret
		ret.Username = creds.Username
	}
	return ret, nil
}
