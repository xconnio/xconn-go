package xconn

import (
	"fmt"
	"reflect"
)

// InvocationParser provides a fluent interface for parsing WAMP invocations.
type InvocationParser struct {
	inv *Invocation
	err error
}

// NewInvocationParser creates a new parser for the given invocation.
func NewInvocationParser(inv *Invocation) *InvocationParser {
	return &InvocationParser{inv: inv}
}

func (p *InvocationParser) AllowRole(allowedRoles ...string) *InvocationParser {
	if p.err != nil {
		return p
	}

	if len(allowedRoles) == 0 {
		p.err = fmt.Errorf("no roles specified for authorization")
		return p
	}

	if len(allowedRoles) == 0 {
		p.err = fmt.Errorf("no roles specified for authorization")
		return p
	}

	callerRoles := p.extractCallerRoles()
	if len(callerRoles) == 0 {
		p.err = fmt.Errorf("no caller roles found in invocation details")
		return p
	}

	// Check if caller has any of the allowed roles
	for _, callerRole := range callerRoles {
		for _, allowedRole := range allowedRoles {
			if callerRole == allowedRole {
				return p // Authorized
			}
		}
	}

	p.err = fmt.Errorf("access denied: caller roles %v not in allowed roles %v", callerRoles, allowedRoles)
	return p
}

func (p *InvocationParser) extractCallerRoles() []string {
	if p.inv.Details == nil {
		return nil
	}

	var roles []string
	if value, exists := p.inv.Details["caller_authrole"]; exists {
		switch v := value.(type) {
		case string:
			if v != "" {
				roles = append(roles, v)
			}
		}
	}

	return roles
}

// Args maps positional arguments to the provided pointers
// Usage: parser.Args(&name, &city, &age).
func (p *InvocationParser) Args(targets ...any) *InvocationParser {
	if p.err != nil {
		return p
	}

	if len(targets) > len(p.inv.Args) {
		p.err = fmt.Errorf("not enough arguments: expected %d, got %d", len(targets), len(p.inv.Args))
		return p
	}

	for i, target := range targets {
		if err := assignValue(p.inv.Args[i], target); err != nil {
			p.err = fmt.Errorf("failed to assign arg[%d]: %w", i, err)
			return p
		}
	}

	return p
}

// ArgsToStruct maps all args to struct fields by position
// The struct should have fields that match the args in order.
func (p *InvocationParser) ArgsToStruct(target any) *InvocationParser {
	if p.err != nil {
		return p
	}

	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		p.err = fmt.Errorf("target must be a pointer to struct")
		return p
	}

	structVal := rv.Elem()
	structType := structVal.Type()

	numFields := structVal.NumField()
	if len(p.inv.Args) > numFields {
		p.err = fmt.Errorf("too many args for struct: got %d args, struct has %d fields", len(p.inv.Args), numFields)
		return p
	}

	for i, arg := range p.inv.Args {
		if i >= numFields {
			break
		}

		field := structVal.Field(i)
		if !field.CanSet() {
			continue // Skip unexported fields
		}

		if err := assignValueToReflectValue(arg, field); err != nil {
			p.err = fmt.Errorf("failed to assign arg[%d] to field %s: %w", i, structType.Field(i).Name, err)
			return p
		}
	}

	return p
}

// Kwargs maps keyword arguments to struct fields by json tag or field name.
func (p *InvocationParser) Kwargs(target any) *InvocationParser {
	if p.err != nil {
		return p
	}

	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Struct {
		p.err = fmt.Errorf("target must be a pointer to struct")
		return p
	}

	structVal := rv.Elem()
	structType := structVal.Type()

	for i := 0; i < structVal.NumField(); i++ {
		field := structVal.Field(i)
		fieldType := structType.Field(i)

		if !field.CanSet() {
			continue // Skip unexported fields
		}

		// Try to get the key from json tag first, then use field name
		key := fieldType.Tag.Get("json")
		if key == "" || key == "-" {
			key = fieldType.Name
		}

		if value, exists := p.inv.Kwargs[key]; exists {
			if err := assignValueToReflectValue(value, field); err != nil {
				p.err = fmt.Errorf("failed to assign kwargs[%s] to field %s: %w", key, fieldType.Name, err)
				return p
			}
		}
	}

	return p
}

// KwargsSpecific maps specific kwargs keys to provided pointers
// Usage: parser.KwargsSpecific(map[string]any{"name": &name, "city": &city}).
func (p *InvocationParser) KwargsSpecific(mapping map[string]any) *InvocationParser {
	if p.err != nil {
		return p
	}

	for key, target := range mapping {
		if value, exists := p.inv.Kwargs[key]; exists {
			if err := assignValue(value, target); err != nil {
				p.err = fmt.Errorf("failed to assign kwargs[%s]: %w", key, err)
				return p
			}
		}
	}

	return p
}

// Validate returns any error that occurred during parsing.
func (p *InvocationParser) Validate() error {
	return p.err
}

// Helper function to assign a value to a pointer target.
func assignValue(source any, target any) error {
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Ptr {
		return fmt.Errorf("target must be a pointer")
	}

	return assignValueToReflectValue(source, rv.Elem())
}

// Helper function to assign a value to a reflect.Value.
func assignValueToReflectValue(source any, target reflect.Value) error {
	if !target.CanSet() {
		return fmt.Errorf("target cannot be set")
	}

	sourceVal := reflect.ValueOf(source)
	targetType := target.Type()

	// Direct assignment if types match
	if sourceVal.Type().AssignableTo(targetType) {
		target.Set(sourceVal)
		return nil
	}

	// Type conversion if possible
	if sourceVal.Type().ConvertibleTo(targetType) {
		target.Set(sourceVal.Convert(targetType))
		return nil
	}

	return fmt.Errorf("cannot assign %T to %s", source, targetType)
}
