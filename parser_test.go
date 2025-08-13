package xconn_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

var (
	testArgs   = []any{"john", "wick", 34} //nolint:gochecknoglobals
	testKwargs = map[string]any{           //nolint:gochecknoglobals
		"location": "NYC",
		"name":     "John Wick",
		"trilogy":  true,
	}
)

func checkArgsKwargs(t *testing.T, obj interface {
	Args() []any
	Kwargs() map[string]any
	ArgsStruct(any) error
	KwargsStruct(any) error
}) {
	require.Equal(t, testArgs, obj.Args())
	require.Equal(t, testKwargs, obj.Kwargs())

	t.Run("args-struct", func(t *testing.T) {
		type User struct {
			FirstName string `json:"0"`
			LastName  string `json:"1"`
			Age       int    `json:"2"`
		}

		var user User
		require.NoError(t, obj.ArgsStruct(&user))
		require.Equal(t, "john", user.FirstName)
		require.Equal(t, "wick", user.LastName)
		require.Equal(t, 34, user.Age)
	})

	t.Run("kwargs-struct", func(t *testing.T) {
		type Movie struct {
			Location string `json:"location"`
			Name     string `json:"name"`
			Trilogy  bool   `json:"trilogy"`
		}

		var movie Movie
		require.NoError(t, obj.KwargsStruct(&movie))
		require.Equal(t, "NYC", movie.Location)
		require.Equal(t, "John Wick", movie.Name)
		require.True(t, movie.Trilogy)
	})
}

func TestInvocation(t *testing.T) {
	details := map[string]any{
		"procedure": "hello",
		"caller":    20000,
	}

	inv := xconn.NewInvocation(testArgs, testKwargs, details)

	checkArgsKwargs(t, inv)

	t.Run("details", func(t *testing.T) {
		require.Equal(t, "hello", inv.Procedure())
		require.EqualValues(t, 20000, inv.Caller())
	})
}

func TestEvent(t *testing.T) {
	details := map[string]any{
		"topic":     "hello",
		"publisher": 20000,
	}

	event := xconn.NewEvent(testArgs, testKwargs, details)

	checkArgsKwargs(t, event)

	t.Run("details", func(t *testing.T) {
		require.Equal(t, "hello", event.Topic())
		require.EqualValues(t, 20000, event.Publisher())
	})
}
