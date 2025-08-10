package xconn_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func TestInvocation(t *testing.T) {
	args := []any{"john", "wick", 34}
	kwargs := map[string]any{
		"location": "NYC",
		"name":     "John Wick",
		"trilogy":  true,
	}
	details := map[string]any{
		"procedure": "hello",
		"caller":    20000,
	}

	inv := xconn.NewInvocation(args, kwargs, details)

	require.Equal(t, args, inv.Args())
	require.Equal(t, kwargs, inv.Kwargs())
	require.Equal(t, details, inv.Details())

	t.Run("args-struct", func(t *testing.T) {
		type User struct {
			FirstName string `json:"0"`
			LastName  string `json:"1"`
			Age       int    `json:"2"`
		}

		var user User
		err := inv.ArgsStruct(&user)
		require.NoError(t, err)

		require.Equal(t, user.FirstName, "john")
		require.Equal(t, user.LastName, "wick")
		require.Equal(t, user.Age, 34)
	})

	t.Run("kwargs-struct", func(t *testing.T) {
		type Movie struct {
			Location string `json:"location"`
			Name     string `json:"name"`
			Trilogy  bool   `json:"trilogy"`
		}

		var movie Movie
		err := inv.KwargsStruct(&movie)
		require.NoError(t, err)

		require.Equal(t, movie.Location, "NYC")
		require.Equal(t, movie.Name, "John Wick")
		require.Equal(t, movie.Trilogy, true)
	})

	t.Run("details", func(t *testing.T) {
		require.Equal(t, "hello", inv.Procedure())
		require.EqualValues(t, 20000, inv.Caller())
	})
}
