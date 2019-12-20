package core

import (
	"strings"

	"github.com/scaleway/scaleway-cli/internal/args"

	"github.com/scaleway/scaleway-sdk-go/logger"
	"github.com/scaleway/scaleway-sdk-go/strcase"
	"github.com/scaleway/scaleway-sdk-go/validation"
)

// CommandValidateFunc validates en entire command.
// Used in core.cobraRun().
type CommandValidateFunc func(cmd *Command, cmdArgs interface{}) error

// ArgSpecValidateFunc validates one argument of a command.
type ArgSpecValidateFunc func(argSpec *ArgSpec, value interface{}) error

// DefaultCommandValidateFunc is the default validation function for commands.
func DefaultCommandValidateFunc() CommandValidateFunc {
	return func(cmd *Command, cmdArgs interface{}) error {
		err := validateArgValues(cmd, cmdArgs)
		if err != nil {
			return err
		}
		err = validateRequiredArgs(cmd, cmdArgs)
		if err != nil {
			return err
		}
		return nil
	}
}

// validateArgValues validates values passed to the different args of a Command.
func validateArgValues(cmd *Command, cmdArgs interface{}) error {
	for _, argSpec := range cmd.ArgSpecs {

		fieldName := strings.ReplaceAll(strcase.ToPublicGoName(argSpec.Name), ".{idx}", "")
		fieldName = strings.ReplaceAll(fieldName, ".{key}", "")
		fieldValue, fieldExists := getValueForFieldByName(cmdArgs, fieldName)
		if !fieldExists {
			logger.Warningf("could not validate arg value for '%v': invalid fieldName: %v", argSpec.Name, fieldName)
			continue
		}

		if argSpec.Required == false && !(!fieldValue.IsNil() || !fieldValue.IsZero() || fieldValue.Interface() != "") {
			return nil
		}

		validateFunc := DefaultArgSpecValidateFunc()
		if argSpec.ValidateFunc != nil {
			validateFunc = argSpec.ValidateFunc
		}
		logger.Debugf("validateArgValues(): argspec=%v, value=%v", argSpec.Name, fieldValue.Interface())

		err := validateFunc(argSpec, fieldValue.Interface())
		if err != nil {
			return err
		}
	}
	return nil
}

// validateRequiredArgs checks for missing required args with no default value.
// Returns an error for the first missing required arg.
// Returns nil otherwise.
func validateRequiredArgs(cmd *Command, cmdArgs interface{}) error {
	for _, arg := range cmd.ArgSpecs {
		fieldName := strcase.ToPublicGoName(arg.Name)
		fieldIsZero, fieldExists := isFieldZero(cmdArgs, fieldName)
		if arg.Required && (fieldIsZero || !fieldExists) {
			return MissingRequiredArgumentError(arg.Name)
		}
	}
	return nil
}

// DefaultArgSpecValidateFunc validates a value passed for an ArgSpec
// Uses ArgSpec.EnumValues
func DefaultArgSpecValidateFunc() ArgSpecValidateFunc {
	return func(argSpec *ArgSpec, value interface{}) error {

		logger.Debugf("value: '%v'", value)

		if len(argSpec.EnumValues) < 1 {
			return nil
		}

		strValue, err := args.MarshalValue(value)
		if err != nil {
			return err
		}

		logger.Debugf("strValue: '%v'", strValue)

		if !stringExists(argSpec.EnumValues, strValue) /*&& strValue != "" && argSpec.Required == false*/ {
			return InvalidValueForEnumError(argSpec.Name, argSpec.EnumValues, strValue)
		}
		return nil
	}
}

func stringExists(strs []string, s string) bool {
	for _, s2 := range strs {
		if s == s2 {
			return true
		}
	}
	return false
}

func ValidateSecretKey() ArgSpecValidateFunc {
	return func(argSpec *ArgSpec, valueI interface{}) error {
		value := valueI.(string)
		err := DefaultArgSpecValidateFunc()(argSpec, value)
		if err != nil {
			return err
		}
		if !validation.IsSecretKey(value) {
			return InvalidSecretKeyError(value)
		}
		return nil
	}
}

func ValidateOrganisationID() ArgSpecValidateFunc {
	return func(argSpec *ArgSpec, valueI interface{}) error {
		value := valueI.(string)
		err := DefaultArgSpecValidateFunc()(argSpec, value)
		if err != nil {
			return err
		}
		if !validation.IsOrganizationID(value) {
			return InvalidOrganizationIdError(value)
		}
		return nil
	}
}
