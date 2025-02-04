// Package cmd implements basic use cases to manage your files on your remote server
// via the command line of your local workstation or any server you can access with SSH.
// It also demonstrates what can be achieved when combining the use of the Go SDK for Cells
// with the powerful Cobra framework to implement CLI client applications for Cells.
package cmd

import (
	"io/ioutil"
	"log"
	"os"
	"strings"

	"github.com/ory/viper"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/pydio/cells-client/v2/common"
	"github.com/pydio/cells-client/v2/rest"
)

const (
	// EnvPrefix represents the prefix used to insure we have a reserved namespacce for cec specific ENV vars.
	EnvPrefix = "CEC"

	confFileName = "config.json"
)

var (
	// These commands and respective children do not need an already configured environment.
	infoCommands = []string{"help", "configure", "version", "completion", "oauth", "clear", "doc", "update", "token", "--help", "config"}

	configFilePath string

	serverURL string
	token     string
	authType  string
	login     string
	password  string

	skipKeyring bool
	skipVerify  bool
	noCache     bool
)

// RootCmd is the parent of all commands defined in this package.
// It takes care of the pre-configuration of the default connection to the SDK in its PersistentPreRun phase.
var RootCmd = &cobra.Command{
	Use:                    os.Args[0],
	Short:                  "Connect to a Pydio Cells server using the command line",
	BashCompletionFunction: bashCompletionFunc,
	Args:                   cobra.MinimumNArgs(1),
	Long: `
DESCRIPTION

  This command line client allows interacting with a Pydio Cells server via the command line. 
  It uses the Cells SDK for Go and the REST API under the hood.

  See the respective help pages of the various commands to get detailed explanation and some examples.

CONFIGURE

  For the very first run, use '` + os.Args[0] + ` configure' to begin the command-line based configuration wizard. 
  This will guide you through a quick procedure to get you up and ready in no time.

  Non-sensitive information are stored by default in a ` + confFileName + ` file under ` + rest.DefaultConfigDirPath() + `
  You can change this location by using the --config flag.
  Entered (or retrieved, in the case of OAuth2 procedure) credentials will be stored in your keyring.

  [Note]: if no keyring is found, all information are stored in clear text in the ` + confFileName + ` file, including sensitive bits.

ENVIRONMENT

  All the command flags documented below are mapped to their associated ENV var, using upper case and CEC_ prefix.

  For example:
    $ ` + os.Args[0] + ` ls --no_cache
  is equivalent to: 
    $ export CEC_NO_CACHE=true; ` + os.Args[0] + ` ls
   
  This is typically useful when using the Cells Client non-interactively on a server:
    $ export CEC_URL=https://files.example.com; export CEC_TOKEN=<Your Personal Access Token>; 
    $ ` + os.Args[0] + ` ls

`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {

		needSetup := true

		for _, skip := range infoCommands { // info commands do not require a configured env.
			if os.Args[1] == skip {
				needSetup = false
				break
			}
		}

		// Manually bind to viper instead of flags.StringVar, flags.BoolVar, etc
		// => This is useful to ease implementation of retro-compatibility

		// We retrieve the config path at this point so that if none is explicitly defined,
		// we can build the default path using AppName that might have been overriden by an extending app.
		parPath := viper.GetString("config")
		if parPath == "" {
			parPath = rest.DefaultConfigDirPath()
		}
		configFilePath = parPath + "/" + confFileName

		tmpURLStr := viper.GetString("url")
		if tmpURLStr != "" {
			// Also sanitize the passed URL
			var err error
			serverURL, err = rest.CleanURL(tmpURLStr)
			if err != nil {
				log.Fatalf("server URL %s seems to be unvalid, please double check and adapt. Cause: %s", tmpURLStr, err.Error())
			}
		}
		authType = viper.GetString("auth_type")
		token = viper.GetString("token")
		login = viper.GetString("login")
		password = viper.GetString("password")
		noCache = viper.GetBool("no_cache")
		skipKeyring = viper.GetBool("skip_keyring")
		skipVerify = viper.GetBool("skip_verify")

		if needSetup {
			e := setUpEnvironment()
			if e != nil {
				if !os.IsNotExist(e) {
					log.Fatalf("unexpected error during initialisation phase: %s", e.Error())
				}
				// TODO Directly launch necessary configure command
				log.Fatalf("No configuration has been found, please make sure to run '%s configure' first.\n", os.Args[0])
			}
		}
	},

	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func init() {
	handleLegagyParams()
	viper.SetEnvPrefix(EnvPrefix)
	viper.AutomaticEnv()

	flags := RootCmd.PersistentFlags()

	flags.String("config", "", "Location of Cells Client's config files, usually "+rest.DefaultConfigDirPath())
	// flags.String("config", rest.DefaultConfigDirPath(), "Location of Cells Client's config files")
	flags.StringP("url", "u", "", "The full URL of the target server")
	flags.StringP("token", "t", "", "A valid Personal Access Token")
	flags.String("login", "", "The user login, for Client auth only")
	flags.String("password", "", "The user password, for Client auth only")

	flags.Bool("skip_verify", false, "By default the Cells Client verifies the validity of TLS certificates for each communication. This option skips TLS certificate verification")
	flags.Bool("skip_keyring", false, "Explicitly tell the tool to *NOT* try to use a keyring, even if present. Warning: sensitive information will be stored in clear text")
	flags.Bool("no_cache", false, "Force token refresh at each call. This might slow down scripts with many calls")

	// Unused for the time being
	// flags.StringP("auth_type", "a", "", "Authorization mechanism used: Personnal Access Token (Default), OAuth2 flow or Client Credentials")
	// flags.MarkHidden("auth_type")

	bindViperFlags(flags, map[string]string{})
}

// SetUpEnvironment configures the current runtime by setting the SDK Config that is used by child commands.
// It first tries to retrieve parameters via flags or environment variables. If it is not enough to define a valid connection,
// we check for a locally defined configuration file (that might also relies on local keyring to store sensitive info).
func setUpEnvironment() error {

	if configFilePath != "" { // override default location for the configuration file
		rest.SetConfigFilePath(configFilePath)
	}

	// First Check if an environment is defined via the context (flags or ENV vars)
	c := getCecConfigFromEnv()

	if c.Url == "" {

		// First check that we have a configuration file
		_, err := ioutil.ReadFile(configFilePath)
		if err != nil {
			return err
		}

		cl, err := rest.GetConfigList()
		if err != nil {
			return err
		}

		activeConfig, err := cl.GetActiveConfig()
		if err != nil {
			return err
		}
		c = *activeConfig

		// Refresh token if required
		if refreshed, err := rest.RefreshIfRequired(&c); refreshed {
			if err != nil {
				log.Fatal("Could not refresh authentication token:", err)
			}
			// Copy config as IdToken will be cleared
			storeConfig := c
			rest.UpdateConfig(&storeConfig)
		}
	}

	// Store current computed config in a public static singleton
	rest.DefaultConfig = &c

	return nil
}

// getCecConfigFromEnv first check if a valid connection has been configured with flags and/or ENV var
// **before** it even tries to retrieve info for the local file configuration.
// Also note that if both Token and User/Pwd are defined, we rather use PAT auth.
func getCecConfigFromEnv() rest.CecConfig {

	// Flags and env variable have been managed by viper => we can rely on local variable
	c := new(rest.CecConfig)
	validConfViaContext := false

	if len(serverURL) > 0 {
		if len(token) > 0 { // PAT auth
			authType = common.PatType
			c.IdToken = token
			validConfViaContext = true
		} else if len(login) > 0 && len(password) > 0 { // client auth
			authType = common.ClientAuthType
			c.Password = password
			c.User = login
			validConfViaContext = true
		}
		// OAuth via ENV vars seems to be irrelevant for v2.1
		// } else if len(idToken) > 0 && len(refreshToken) {
		// 	authType = common.OAuthType
		// 	validConfViaContext = true
	}

	if !validConfViaContext {
		return *c
	}

	c.Url = serverURL
	c.AuthType = authType

	c.SkipVerify = skipVerify
	c.SkipKeyring = skipKeyring
	c.UseTokenCache = !noCache

	return *c
}

// handleLegagyParams manages backward compatibility for ENV variables and flags.
func handleLegagyParams() {

	prefOld := "CELLS_CLIENT_TARGET_"

	for _, pair := range os.Environ() {
		if strings.HasPrefix(pair, prefOld) {
			parts := strings.Split(pair, "=")
			if len(parts) == 2 && parts[1] != "" {
				switch parts[0] {
				case "CELLS_CLIENT_TARGET_URL":
					os.Setenv("CEC_URL", parts[1])
				case "CELLS_CLIENT_TARGET_CLIENT_KEY", "CELLS_CLIENT_TARGET_CLIENT_SECRET":
					log.Printf("[WARNING] %s is not used anymore. Double check your configuration", parts[0])
				case "CELLS_CLIENT_TARGET_USER_LOGIN":
					os.Setenv("CEC_LOGIN", parts[1])
				case "CELLS_CLIENT_TARGET_USER_PWD":
					os.Setenv("CEC_PASSWORD", parts[1])
				case "CELLS_CLIENT_TARGET_SKIP_VERIFY":
					os.Setenv("CEC_SKIP_VERIFY", parts[1])
				}
			}
		}
	}
}

// bindViperFlags visits all flags in FlagSet and bind their key to the corresponding viper variable.
func bindViperFlags(flags *pflag.FlagSet, replaceKeys map[string]string) {
	flags.VisitAll(func(flag *pflag.Flag) {
		key := flag.Name
		if replace, ok := replaceKeys[flag.Name]; ok {
			key = replace
		}
		viper.BindPFlag(key, flag)
	})
}

var bashCompletionFunc = `__` + os.Args[0] + `_custom_func() {
  case ${last_command} in
  ` + os.Args[0] + `_mv | ` + os.Args[0] + `_cp | ` + os.Args[0] + `_rm | ` + os.Args[0] + `_ls)
    _path_completion
    return
    ;;
	` + os.Args[0] + `_storage_resync-ds)
    _datasources_completion
    return
    ;;
  ` + os.Args[0] + `_scp)
    _scp_path_completion
    return
    ;;
  *) ;;
  esac
}
_path_completion() {
  local lsopts cur dir
  cur="${COMP_WORDS[COMP_CWORD]}"
  dir="$(dirname "$cur" 2>/dev/null)"

  currentlength=${#cur}
  last_char=${cur:currentlength-1:1}

  if [[ $last_char == "/" ]] && [[ currentlength -gt 2 ]]; then
    dir=$cur
  elif [[ -z $dir ]]; then
    dir="/"
  elif [[ $dir == "." ]]; then
    dir="/"
  fi

  IFS=$'\n'
  lsopts="$(` + os.Args[0] + ` ls --raw $dir)"

  COMPREPLY=($(compgen -W "${lsopts[@]}" -- "$cur"))
  compopt -o nospace
  compopt -o filenames
}

_scp_path_completion() {
  local lsopts cur dir
  cur="${COMP_WORDS[COMP_CWORD]}"
	
  if [[ $cur != cells//* ]]; then
    return
  fi

  prefix="cells//"
  cur=${cur#$prefix}

  dir="$(dirname "$cur" 2>/dev/null)"
  currentlength=${#cur}
  last_char=${cur:currentlength-1:1}

  if [[ $last_char == "/" ]] && [[ currentlength -gt 2 ]]; then
      dir=$cur
  elif [[ -z $dir ]]; then
      dir="/"
  elif [[ $dir == "." ]]; then
      dir="/"
  fi

  IFS=$'\n'
  lsopts="$(` + os.Args[0] + ` ls --raw $dir)"

  COMPREPLY=($(compgen -P "$prefix" -W "${lsopts[@]}" -- "$cur"))
  #COMPREPLY=(${COMPREPLY[@]/#/"${prefix}"})
  compopt -o nospace
  compopt -o filenames
}

_datasources_completion() {
  local dsopts cur
  cur="${COMP_WORDS[COMP_CWORD]}"

  dsopts="$(` + os.Args[0] + ` storage list-datasources --raw)"
  COMPREPLY=($(compgen -W "${dsopts[@]}" -- "$cur"))
}
`
