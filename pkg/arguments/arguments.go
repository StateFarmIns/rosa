/*
Copyright (c) 2020 Red Hat, Inc.

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

// This file contains functions that add common arguments to the command line.

package arguments

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/openshift/rosa/pkg/aws/profile"
	"github.com/openshift/rosa/pkg/aws/region"
	"github.com/openshift/rosa/pkg/debug"
)

var hasUnknownFlags bool

// ParseUnknownFlags parses all flags from the CLI, including
// unknown ones, and adds them to the current command tree
func ParseUnknownFlags(cmd *cobra.Command, argv []string) error {
	flags := cmd.Flags()

	prevArg := ""
	for _, arg := range argv {
		// If there are two consecutive flags, assume we've already
		// dealt with the previous one by setting it as 'true'.
		if strings.HasPrefix(arg, "-") && prevArg != "" {
			var boolVal bool
			flags.BoolVar(&boolVal, prevArg, false, "")
			flags.Set(prevArg, "true")
			prevArg = ""
			hasUnknownFlags = true
		}

		switch {
		// A long flag with a space separated value
		case strings.HasPrefix(arg, "--") && !strings.Contains(arg, "="):
			arg = arg[2:]
			// Skip EOF and known flags
			if len(arg) == 0 || flags.Lookup(arg) != nil {
				continue
			}
			prevArg = arg
			continue
		// The value for the previous flag
		case prevArg != "":
			var strVal string
			flags.StringVar(&strVal, prevArg, "", "")
			flags.Set(prevArg, arg)
			prevArg = ""
			hasUnknownFlags = true
			continue
		// A long flag with an '=' separated value
		case strings.HasPrefix(arg, "--") && strings.Contains(arg, "="):
			val := strings.SplitN(arg[2:], "=", 2)
			// Only consider unknown flags with values
			if len(val) == 2 && flags.Lookup(val[0]) == nil {
				var strVal string
				flags.StringVar(&strVal, val[0], "", "")
				flags.Set(val[0], val[1])
				hasUnknownFlags = true
			}
			continue
		}
	}

	err := flags.Parse(argv)
	if err != nil {
		return err
	}

	// If help is called, regardless of other flags, return we want help.
	// Also say we need help if the command isn't runnable.
	helpVal, err := cmd.Flags().GetBool("help")
	if err != nil {
		// should be impossible to get here as we always declare a help
		// flag in InitDefaultHelpFlag()
		cmd.Println("\"help\" flag declared as non-bool. Please correct your code")
		return err
	}
	if helpVal {
		return pflag.ErrHelp
	}

	return nil
}

// Parse known flags will take the command line arguments and map the ones that fit with known flags.
func ParseKnownFlags(cmd *cobra.Command, argv []string, failOnUnknown bool) error {
	flags := cmd.Flags()

	var validArgs []string = []string{}
	var upcomingValue bool
	unknownFlags := ""

	for _, arg := range argv {
		switch {
		// A long flag with a space separated value
		case strings.HasPrefix(arg, "--") && !strings.Contains(arg, "="):
			flagName := arg[2:]
			// Skip EOF and known flags
			if flag := flags.Lookup(flagName); flag != nil {
				validArgs = append(validArgs, arg)
				if flag.Value.Type() != "bool" {
					upcomingValue = true
				}
			} else if failOnUnknown {
				unknownFlags += fmt.Sprintf("%q, ", flagName)
			}
		// A long flag with a value after an equal sign
		case strings.HasPrefix(arg, "--") && strings.Contains(arg, "="):
			flagName := strings.SplitN(arg[2:], "=", 2)[0]
			if flags.Lookup(flagName) != nil {
				validArgs = append(validArgs, arg)
			} else if failOnUnknown {
				unknownFlags += fmt.Sprintf("%q, ", flagName)
			}
			upcomingValue = false
		// A short flag with a space separated value
		case strings.HasPrefix(arg, "-") && !strings.Contains(arg, "="):
			flagName := arg[1:]
			if flag := flags.Lookup(flagName); flag != nil {
				validArgs = append(validArgs, arg)
				if flag.Value.Type() != "bool" {
					upcomingValue = true
				}
			} else if failOnUnknown {
				unknownFlags += fmt.Sprintf("%q, ", flagName)
			}
		// A short flag with with a value after an equal sign
		case strings.HasPrefix(arg, "-") && strings.Contains(arg, "="):
			flagName := strings.SplitN(arg[1:], "=", 2)[0]
			if flags.Lookup(flagName) != nil {
				validArgs = append(validArgs, arg)
			} else if failOnUnknown {
				unknownFlags += fmt.Sprintf("%q, ", flagName)
			}
			upcomingValue = false
		case upcomingValue:
			validArgs = append(validArgs, arg)
			upcomingValue = false
		}
	}

	if failOnUnknown && unknownFlags != "" {
		return fmt.Errorf("Unknown flags passed: %s", unknownFlags[:len(unknownFlags)-2])
	}

	err := flags.Parse(validArgs)
	if err != nil {
		return err
	}

	// If help is called, regardless of other flags, return we want help.
	// Also say we need help if the command isn't runnable.
	helpVal, err := cmd.Flags().GetBool("help")
	if err != nil {
		// should be impossible to get here as we always declare a help
		// flag in InitDefaultHelpFlag()
		cmd.Println("\"help\" flag declared as non-bool. Please correct your code")
		return err
	}
	if helpVal {
		return pflag.ErrHelp
	}

	return nil
}

func AddStringFlag(cmd *cobra.Command, flagName string) {
	flags := cmd.Flags()
	var pStrVal *string = new(string)
	flags.StringVar(pStrVal, flagName, "", "")
}

// HasUnknownFlags returns whether the flag parser detected any unknown flags
func HasUnknownFlags() bool {
	return hasUnknownFlags
}

// AddDebugFlag adds the '--debug' flag to the given set of command line flags.
func AddDebugFlag(fs *pflag.FlagSet) {
	debug.AddFlag(fs)
}

// AddProfileFlag adds the '--profile' flag to the given set of command line flags.
func AddProfileFlag(fs *pflag.FlagSet) {
	profile.AddFlag(fs)
}

func GetProfile() string {
	return profile.Profile()
}

// AddRegionFlag adds the '--region' flag to the given set of command line flags.
func AddRegionFlag(fs *pflag.FlagSet) {
	region.AddFlag(fs)
}

func GetRegion() string {
	return region.Region()
}

func IsValidMode(modes []string, mode string) bool {
	for _, modeValue := range modes {
		if mode == modeValue {
			return true
		}
	}
	return false
}
