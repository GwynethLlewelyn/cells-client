package rest

import (
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/pydio/cells-client/v2/common"
)

// NoKeyringMsg warns end user when no keyring is found
const NoKeyringMsg = "Could not access local keyring: sensitive information like token or password will end up stored in clear text in the client machine."

func getKeyringServiceName() string {
	return "com.pydio." + common.AppName
}

// ConfigToKeyring stores sensitive information in local keyring if any and removes it from current SDK config.
func ConfigToKeyring(conf *CecConfig) error {

	currKey := key(conf.Url, conf.User)
	switch conf.AuthType {
	case common.PatType:
		if e := keyring.Set(getKeyringServiceName(), currKey, conf.IdToken); e != nil {
			return e
		}
		conf.IdToken = ""
	case common.OAuthType:
		value := value(conf.IdToken, conf.RefreshToken)
		if e := keyring.Set(getKeyringServiceName(), currKey, value); e != nil {
			return e
		}
		conf.IdToken = ""
		conf.RefreshToken = ""
	case common.ClientAuthType:
		if e := keyring.Set(getKeyringServiceName(), currKey, conf.Password); e != nil {
			return e
		}
		conf.Password = ""
	}
	return nil
}

// ConfigFromKeyring tries to find sensitive info inside local keychain and feed the conf.
func ConfigFromKeyring(conf *CecConfig) error {
	value, err := keyring.Get(getKeyringServiceName(), key(conf.Url, conf.User))
	if err != nil {
		// Best effort to retrieve legacy conf
		err = retrieveLegacyKey(conf)
		if err != nil {
			return err
		}
		value, err = keyring.Get(getKeyringServiceName(), key(conf.Url, conf.User))
		if err != nil {
			return err
		}
	}

	switch conf.AuthType {
	case common.OAuthType:
		parts := splitValue(value)
		conf.IdToken = parts[0]
		conf.RefreshToken = parts[1]
	case common.ClientAuthType:
		conf.Password = value
	case common.PatType:
		conf.IdToken = value
	}
	return nil
}

// CheckKeyring simply tries a write followed by a read in the local keyring and
// returns nothing if it works or an error otherwise.
func CheckKeyring() error {

	fmt.Println("Checking keyring service for", getKeyringServiceName())

	testKey := key("https://test.example.com", "john.doe")
	testValue := "A very complicated value !!#%<{}//\\q"

	if e := keyring.Set(getKeyringServiceName(), testKey, testValue); e != nil {
		return e
	}

	defer func() {
		// Best effort to remove the test key from the keyring => ignore error
		_ = keyring.Delete(getKeyringServiceName(), testKey)
	}()

	value, err := keyring.Get(getKeyringServiceName(), testKey)
	if err != nil {
		return err
	}

	if value != testValue {
		return fmt.Errorf("keyring seems to be broken in this machine, retrieved value (%s) differs from the one we stored (%s)", value, testValue)
	}

	return nil
}

const (
	keySep   = "::"
	valueSep = "__//__"
)

func key(prefix, suffix string) string {
	return fmt.Sprintf("%s%s%s", prefix, keySep, suffix)
}

func value(prefix, suffix string) string {
	return fmt.Sprintf("%s%s%s", prefix, valueSep, suffix)
}

func splitValue(value string) []string {
	return strings.Split(value, valueSep)
}

// ClearKeyring removes sensitive info from the local keychain, if they are present.
func ClearKeyring(c *CecConfig) error {
	// Best effort to remove known keys from keyring
	if err := keyring.Delete(getKeyringServiceName(), key(c.Url, c.User)); err != nil {
		if err.Error() != "secret not found in keyring" {
			return err
		}
	}
	return nil
}

func retrieveLegacyKey(conf *CecConfig) error {
	if conf.User != "" && conf.Password == "" { // client auth
		if value, e := keyring.Get(getKeyringServiceName(), key(conf.Url, "ClientCredentials")); e == nil {
			parts := splitValue(value)
			//conf.ClientSecret = parts[0]
			conf.Password = parts[1]
			conf.AuthType = common.ClientAuthType
			// Leave the keyring in a clean state
			_ = keyring.Delete(getKeyringServiceName(), key(conf.Url, "ClientCredentials"))
		} else {
			return e
		}
	} else if conf.IdToken == "" && conf.RefreshToken == "" && conf.Password == "" { // oauth
		if value, e := keyring.Get(getKeyringServiceName(), key(conf.Url, "IdToken")); e == nil {
			parts := splitValue(value)
			conf.IdToken = parts[0]
			conf.RefreshToken = parts[1]
			conf.AuthType = common.OAuthType
			RefreshIfRequired(conf)
			_ = keyring.Delete(getKeyringServiceName(), key(conf.Url, "IdToken"))
		} else {
			return e
		}
	}

	return UpdateConfig(conf)
	// DefaultConfig = conf
	// fmt.Printf("%s Legacy configuration will be migrated.\n", promptui.IconGood)
	// saveConfig(conf)

	// return nil
}

// saveConfig handle file and/or keyring storage depending on user preference and system.
// func saveConfig(config *CecConfig) error {

// 	var err error
// 	oldConfig := DefaultConfig
// 	defer func() {
// 		if err != nil {
// 			DefaultConfig = oldConfig
// 		}
// 	}()

// 	DefaultConfig = config

// 	uname, e := RetrieveCurrentSessionLogin()
// 	if e != nil {
// 		return fmt.Errorf("could not connect to distant server with provided parameters. Discarding change")
// 	}
// 	config.User = uname

// 	if !config.SkipKeyring {
// 		if err = ConfigToKeyring(config); err != nil {
// 			// We still save info in clear text but warn the user
// 			fmt.Println(promptui.IconWarn + " " + NoKeyringMsg)
// 			// Force skip keyring flag in the config file to be explicit
// 			config.SkipKeyring = true
// 		}
// 	}

// 	file := GetConfigFilePath()

// 	// Add version before saving the config
// 	config.CreatedAtVersion = common.Version

// 	data, e := json.MarshalIndent(config, "", "\t")
// 	if e != nil {
// 		err = e
// 		return e
// 	}
// 	if err = ioutil.WriteFile(file, data, 0600); err != nil {
// 		return err
// 	}

// 	return nil
// }
