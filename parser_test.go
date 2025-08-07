package xconn_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func TestArgs(t *testing.T) {
	inv := &xconn.Invocation{
		Args: []any{"John", "New York", 30, []byte{1, 2}},
	}

	var name, city string
	var age int
	var data []byte

	err := xconn.NewInvocationParser(inv).Args(&name, &city, &age, &data).Validate()
	require.NoError(t, err)

	require.Equal(t, name, "John")
	require.Equal(t, city, "New York")
	require.Equal(t, age, 30)
	require.Equal(t, data, []byte{1, 2})
}

func TestArgsStruct(t *testing.T) {
	inv := &xconn.Invocation{
		Args: []any{"John", "New York", 30, []byte{1, 2}},
	}

	type Person struct {
		Name string
		City string
		Age  int
		Data []byte
	}

	var person Person
	err := xconn.NewInvocationParser(inv).ArgsToStruct(&person).Validate()
	require.NoError(t, err)
	require.Equal(t, person.Name, "John")
	require.Equal(t, person.City, "New York")
	require.Equal(t, person.Age, 30)
	require.Equal(t, person.Data, []byte{1, 2})
}

func TestKwargs(t *testing.T) {
	inv := &xconn.Invocation{
		Kwargs: map[string]any{
			"name": "Alice",
			"city": "Boston",
			"age":  25,
			"data": []byte{1, 2},
		},
	}

	type Person struct {
		Name string `json:"name"`
		City string `json:"city"`
		Age  int    `json:"age"`
		Data []byte `json:"data"`
	}

	var person Person
	err := xconn.NewInvocationParser(inv).Kwargs(&person).Validate()
	require.NoError(t, err)
	require.Equal(t, person.Name, "Alice")
	require.Equal(t, person.City, "Boston")
	require.Equal(t, person.Age, 25)
	require.Equal(t, person.Data, []byte{1, 2})
}

func TestArgsKwargs(t *testing.T) {
	inv := &xconn.Invocation{
		Args:   []any{"John", "New York", 30, []byte{1, 2}},
		Kwargs: map[string]any{"hometown": "Chicago", "distance": 3500},
	}

	type Person struct {
		Name string
		City string
		Age  int
		Data []byte
	}

	type PersonKwargs struct {
		Hometown string `json:"hometown"`
		Distance int    `json:"distance"`
	}

	var person Person
	var personKwargs PersonKwargs
	err := xconn.NewInvocationParser(inv).
		ArgsToStruct(&person).
		Kwargs(&personKwargs).
		Validate()
	require.NoError(t, err)

	require.Equal(t, person.Name, "John")
	require.Equal(t, person.City, "New York")
	require.Equal(t, person.Age, 30)
	require.Equal(t, person.Data, []byte{1, 2})

	require.Equal(t, personKwargs.Hometown, "Chicago")
	require.Equal(t, personKwargs.Distance, 3500)
}

func TestKwargsSpecific(t *testing.T) {
	inv := &xconn.Invocation{
		Kwargs: map[string]any{
			"username": "alice",
			"city":     "Boston",
			"age":      25,
			"active":   true,
			"score":    98.5,
		},
	}

	var username string
	var age int
	var isActive bool
	var score float64

	err := xconn.NewInvocationParser(inv).KwargsSpecific(map[string]any{
		"username": &username,
		"age":      &age,
		"active":   &isActive,
		"score":    &score,
	}).Validate()
	require.NoError(t, err)

	require.Equal(t, username, "alice")
	require.Equal(t, age, 25)
	require.Equal(t, isActive, true)
	require.Equal(t, score, 98.5)
}

func TestRole(t *testing.T) {
	inv := &xconn.Invocation{
		Details: map[string]any{"caller_authrole": "anon"},
	}

	err := xconn.NewInvocationParser(inv).AllowRole("anon").Validate()
	require.NoError(t, err)

	err = xconn.NewInvocationParser(inv).AllowRole("user").Validate()
	require.Error(t, err)
}

func TestInvalidPayloadNoCrash(t *testing.T) {
	inv := &xconn.Invocation{Args: []any{"John", nil}}

	var name string
	var data []byte

	err := xconn.NewInvocationParser(inv).Args(&name, &data).Validate()
	require.Error(t, err)

	inv = &xconn.Invocation{
		Kwargs: map[string]any{
			"username": nil,
		},
	}

	var username string
	err = xconn.NewInvocationParser(inv).KwargsSpecific(map[string]any{
		"username": &username,
	}).Validate()
	require.Error(t, err)
}
