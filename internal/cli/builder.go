// SPDX-License-Identifier: Apache-2.0
// Copyright 2022 Acorn Labs, Inc; All rights reserved.
// Copyright 2022 Unikraft GmbH; All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package cli

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unsafe"

	"github.com/rancher/wrangler/pkg/signals"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"kraftkit.sh/config"
	"kraftkit.sh/iostreams"
	"kraftkit.sh/log"
)

var (
	caseRegexp    = regexp.MustCompile("([a-z])([A-Z])")
	flagOverrides = make(map[string][]*pflag.Flag)
)

func RegisterFlag(cmdline string, flag *pflag.Flag) {
	flagOverrides[cmdline] = append(flagOverrides[cmdline], flag)
}

type PersistentPreRunnable interface {
	PersistentPre(cmd *cobra.Command, args []string) error
}

type PreRunnable interface {
	Pre(cmd *cobra.Command, args []string) error
}

type Runnable interface {
	Run(cmd *cobra.Command, args []string) error
}

type fieldInfo struct {
	FieldType  reflect.StructField
	FieldValue reflect.Value
}

func fields(obj any) []fieldInfo {
	ptrValue := reflect.ValueOf(obj)
	objValue := ptrValue.Elem()

	var result []fieldInfo

	for i := 0; i < objValue.NumField(); i++ {
		fieldType := objValue.Type().Field(i)
		if fieldType.Anonymous && fieldType.Type.Kind() == reflect.Struct {
			result = append(result, fields(objValue.Field(i).Addr().Interface())...)
		} else if !fieldType.Anonymous {
			result = append(result, fieldInfo{
				FieldValue: objValue.Field(i),
				FieldType:  objValue.Type().Field(i),
			})
		}
	}

	return result
}

func Name(obj any) string {
	ptrValue := reflect.ValueOf(obj)
	objValue := ptrValue.Elem()
	commandName := strings.Replace(objValue.Type().Name(), "Command", "", 1)
	commandName, _ = name(commandName, "", "")
	return commandName
}

func expandRegisteredFlags(cmd *cobra.Command) {
	// Add flag overrides which can be provided by plugins
	for arg, flags := range flagOverrides {
		args := strings.Fields(arg)
		subCmd, _, err := cmd.Traverse(args[1:])
		if err != nil {
			continue
		}

		for _, flag := range flags {
			subCmd.Flags().AddFlag(flag)
		}
	}
}

func Main(cmd *cobra.Command) {
	expandRegisteredFlags(cmd)
	ctx := signals.SetupSignalContext()
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// defaultOptions is a cache of previously instantiated default cli options.
var defaultOptions *CliOptions

func contextualize(ctx context.Context, opts ...CliOption) context.Context {
	if defaultOptions == nil {
		defaultOptions = &CliOptions{}
		for _, o := range []CliOption{
			WithDefaultConfigManager(),
			WithDefaultIOStreams(),
			WithDefaultPackageManager(),
			WithDefaultPluginManager(),
			WithDefaultLogger(),
			WithDefaultHTTPClient(),
		} {
			o(defaultOptions)
		}
	}

	copts := defaultOptions

	// Apply user-specified options and then defaults.  The default options are
	// programmed in a way such to prefer exiting values (set initially by any
	// user-specified options).
	for _, o := range opts {
		o(copts)
	}

	// Set up the config manager in the context if it is available
	cfgm, err := copts.configManager()
	if err == nil {
		ctx = config.WithConfigManager(ctx, cfgm)
	}

	// Set up the logger in the context if it is available
	if copts.logger != nil {
		ctx = log.WithLogger(ctx, copts.logger)
	}

	// Set up the iostreams in the context if it is available
	if copts.ioStreams != nil {
		ctx = iostreams.WithIOStreams(ctx, copts.ioStreams)
	}

	return ctx
}

// New populates a cobra.Command object by extracting args from struct tags of the
// Runnable obj passed.  Also the Run method is assigned to the RunE of the command.
// name = Override the struct field with
func New(obj Runnable, cmd cobra.Command, opts ...CliOption) *cobra.Command {
	var (
		envs      []func()
		arrays    = map[string]reflect.Value{}
		slices    = map[string]reflect.Value{}
		maps      = map[string]reflect.Value{}
		optString = map[string]reflect.Value{}
		optBool   = map[string]reflect.Value{}
		optInt    = map[string]reflect.Value{}
		ptrValue  = reflect.ValueOf(obj)
		objValue  = ptrValue.Elem()
	)

	c := cmd
	if c.Use == "" {
		c.Use = fmt.Sprintf("%s [SUBCOMMAND] [FLAGS]", Name(obj))
	}

	for _, info := range fields(obj) {
		fieldType := info.FieldType
		v := info.FieldValue

		if strings.ToUpper(fieldType.Name[0:1]) != fieldType.Name[0:1] {
			continue
		}

		name, alias := name(fieldType.Name, fieldType.Tag.Get("name"), fieldType.Tag.Get("short"))
		usage := fieldType.Tag.Get("usage")
		env := strings.Split(fieldType.Tag.Get("env"), ",")
		defValue := fieldType.Tag.Get("default")
		if len(env) == 1 && env[0] == "" {
			env = nil
		}
		defInt, err := strconv.Atoi(defValue)
		if err != nil {
			defInt = 0
		}

		flags := c.PersistentFlags()
		if fieldType.Tag.Get("local") == "true" {
			flags = c.LocalFlags()
		}

		switch fieldType.Type.Kind() {
		case reflect.Int:
			flags.IntVarP((*int)(unsafe.Pointer(v.Addr().Pointer())), name, alias, defInt, usage)
		case reflect.Int64:
			flags.IntVarP((*int)(unsafe.Pointer(v.Addr().Pointer())), name, alias, defInt, usage)
		case reflect.String:
			flags.StringVarP((*string)(unsafe.Pointer(v.Addr().Pointer())), name, alias, defValue, usage)
		case reflect.Slice:
			switch fieldType.Tag.Get("split") {
			case "false":
				arrays[name] = v
				flags.StringArrayP(name, alias, nil, usage)
			default:
				slices[name] = v
				flags.StringSliceP(name, alias, nil, usage)
			}
		case reflect.Map:
			maps[name] = v
			flags.StringSliceP(name, alias, nil, usage)
		case reflect.Bool:
			flags.BoolVarP((*bool)(unsafe.Pointer(v.Addr().Pointer())), name, alias, false, usage)
		case reflect.Pointer:
			switch fieldType.Type.Elem().Kind() {
			case reflect.Int:
				optInt[name] = v
				flags.IntP(name, alias, defInt, usage)
			case reflect.String:
				optString[name] = v
				flags.StringP(name, alias, defValue, usage)
			case reflect.Bool:
				optBool[name] = v
				flags.BoolP(name, alias, false, usage)
			}
		default:
			panic("Unknown kind on field " + fieldType.Name + " on " + objValue.Type().Name())
		}

		for _, env := range env {
			envs = append(envs, func() {
				v := os.Getenv(env)
				if v != "" {
					fv, err := flags.GetString(name)
					if err == nil && (fv == "" || fv == defValue) {
						_ = flags.Set(name, v)
					}
				}
			})
		}
	}

	if p, ok := obj.(PersistentPreRunnable); ok {
		c.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
			cmd.SetContext(contextualize(cmd.Context(), opts...))
			return p.PersistentPre(cmd, args)
		}
	} else {
		c.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
			cmd.SetContext(contextualize(cmd.Context(), opts...))
			return nil
		}
	}

	if p, ok := obj.(PreRunnable); ok {
		c.PreRunE = p.Pre
	}

	c.SilenceErrors = true
	c.SilenceUsage = true
	c.DisableFlagsInUseLine = true

	c.RunE = obj.Run
	c.PersistentPreRunE = bind(c.PersistentPreRunE, arrays, slices, maps, optInt, optBool, optString, envs)
	c.PreRunE = bind(c.PreRunE, arrays, slices, maps, optInt, optBool, optString, envs)
	c.RunE = bind(c.RunE, arrays, slices, maps, optInt, optBool, optString, envs)

	// Set help and usage methods
	c.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		rootHelpFunc(cmd, args)
	})
	c.SetUsageFunc(rootUsageFunc)
	c.SetFlagErrorFunc(rootFlagErrorFunc)

	return &c
}

func assignOptBool(app *cobra.Command, maps map[string]reflect.Value) error {
	for k, v := range maps {
		k = contextKey(k)
		if !app.Flags().Lookup(k).Changed {
			continue
		}
		i, err := app.Flags().GetBool(k)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(&i))
	}
	return nil
}

func assignOptString(app *cobra.Command, maps map[string]reflect.Value) error {
	for k, v := range maps {
		k = contextKey(k)
		if !app.Flags().Lookup(k).Changed {
			continue
		}
		i, err := app.Flags().GetString(k)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(&i))
	}
	return nil
}

func assignOptInt(app *cobra.Command, maps map[string]reflect.Value) error {
	for k, v := range maps {
		k = contextKey(k)
		if !app.Flags().Lookup(k).Changed {
			continue
		}
		i, err := app.Flags().GetInt(k)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(&i))
	}
	return nil
}

func assignMaps(app *cobra.Command, maps map[string]reflect.Value) error {
	for k, v := range maps {
		k = contextKey(k)
		s, err := app.Flags().GetStringSlice(k)
		if err != nil {
			return err
		}
		if s != nil {
			values := map[string]string{}
			for _, part := range s {
				parts := strings.SplitN(part, "=", 2)
				if len(parts) == 1 {
					values[parts[0]] = ""
				} else {
					values[parts[0]] = parts[1]
				}
			}
			v.Set(reflect.ValueOf(values))
		}
	}
	return nil
}

func assignSlices(app *cobra.Command, slices map[string]reflect.Value) error {
	for k, v := range slices {
		k = contextKey(k)
		s, err := app.Flags().GetStringSlice(k)
		if err != nil {
			return err
		}
		a := app.Flags().Lookup(k)
		if a.Changed && len(s) == 0 {
			s = []string{""}
		}
		if s != nil {
			v.Set(reflect.ValueOf(s[:]))
		}
	}
	return nil
}

func assignArrays(app *cobra.Command, arrays map[string]reflect.Value) error {
	for k, v := range arrays {
		k = contextKey(k)
		s, err := app.Flags().GetStringArray(k)
		if err != nil {
			return err
		}
		a := app.Flags().Lookup(k)
		if a.Changed && len(s) == 0 {
			s = []string{""}
		}
		if s != nil {
			v.Set(reflect.ValueOf(s[:]))
		}
	}
	return nil
}

func contextKey(name string) string {
	parts := strings.Split(name, ",")
	return parts[len(parts)-1]
}

func name(name, setName, short string) (string, string) {
	if setName != "" {
		return setName, short
	}
	parts := strings.Split(name, "_")
	i := len(parts) - 1
	name = caseRegexp.ReplaceAllString(parts[i], "$1-$2")
	name = strings.ToLower(name)
	result := append([]string{name}, parts[0:i]...)
	for i := 0; i < len(result); i++ {
		result[i] = strings.ToLower(result[i])
	}
	if short == "" && len(result) > 1 {
		short = result[1]
	}
	return result[0], short
}

func bind(next func(*cobra.Command, []string) error,
	arrays map[string]reflect.Value,
	slices map[string]reflect.Value,
	maps map[string]reflect.Value,
	optInt map[string]reflect.Value,
	optBool map[string]reflect.Value,
	optString map[string]reflect.Value,
	envs []func(),
) func(*cobra.Command, []string) error {
	if next == nil {
		return nil
	}
	return func(cmd *cobra.Command, args []string) error {
		for _, envCallback := range envs {
			envCallback()
		}
		if err := assignArrays(cmd, arrays); err != nil {
			return err
		}
		if err := assignSlices(cmd, slices); err != nil {
			return err
		}
		if err := assignMaps(cmd, maps); err != nil {
			return err
		}
		if err := assignOptInt(cmd, optInt); err != nil {
			return err
		}
		if err := assignOptBool(cmd, optBool); err != nil {
			return err
		}
		if err := assignOptString(cmd, optString); err != nil {
			return err
		}

		if next != nil {
			return next(cmd, args)
		}

		return nil
	}
}
