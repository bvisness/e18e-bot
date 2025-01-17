package db

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

func TestTypes(t *testing.T) {
	type CustomInt int
	type S struct {
		I   int       `db:"I"`   // field 0
		I64 int64     `db:"I64"` // field 1
		F   float64   `db:"F"`   // field 2
		B   bool      `db:"B"`   // field 3
		BS  []byte    `db:"BS"`  // field 4
		S   string    `db:"S"`   // field 5
		CI  CustomInt `db:"CI"`  // field 6
		T   time.Time `db:"T"`   // field 7
	}

	names, paths := getColumnNamesAndPaths(reflect.TypeOf(S{}), nil, nil)
	assert.Equal(t, []columnName{
		{"I"}, {"I64"}, {"F"}, {"B"}, {"BS"}, {"S"}, {"CI"}, {"T"},
	}, names)
	assert.Equal(t, []fieldPath{
		{0}, {1}, {2}, {3}, {4}, {5}, {6}, {7},
	}, paths)
	assert.True(t, len(names) == len(paths))
}

func TestPaths(t *testing.T) {
	type CustomInt int
	type S2 struct {
		B  bool  `db:"B"`  // field 0
		PB *bool `db:"PB"` // field 1

		NoTag string // field 2
	}
	type S struct {
		I   int        `db:"I"`   // field 0
		PI  *int       `db:"PI"`  // field 1
		CI  CustomInt  `db:"CI"`  // field 2
		PCI *CustomInt `db:"PCI"` // field 3
		S2  `db:"S2"`  // field 4 (embedded!)
		PS2 *S2        `db:"PS2"` // field 5

		NoTag int // field 6
	}
	type Nested struct {
		S  S  `db:"S"`  // field 0
		PS *S `db:"PS"` // field 1

		NoTag S // field 2
	}
	type Embedded struct {
		NoTag  S // field 0
		Nested   // field 1
	}

	names, paths := getColumnNamesAndPaths(reflect.TypeOf(Embedded{}), nil, nil)
	assert.Equal(t, []columnName{
		{"S", "I"}, {"S", "PI"},
		{"S", "CI"}, {"S", "PCI"},
		{"S", "S2", "B"}, {"S", "S2", "PB"},
		{"S", "PS2", "B"}, {"S", "PS2", "PB"},
		{"PS", "I"}, {"PS", "PI"},
		{"PS", "CI"}, {"PS", "PCI"},
		{"PS", "S2", "B"}, {"PS", "S2", "PB"},
		{"PS", "PS2", "B"}, {"PS", "PS2", "PB"},
	}, names)
	assert.Equal(t, []fieldPath{
		{1, 0, 0}, {1, 0, 1}, // Nested.S.I, Nested.S.PI
		{1, 0, 2}, {1, 0, 3}, // Nested.S.CI, Nested.S.PCI
		{1, 0, 4, 0}, {1, 0, 4, 1}, // Nested.S.S2.B, Nested.S.S2.PB
		{1, 0, 5, 0}, {1, 0, 5, 1}, // Nested.S.PS2.B, Nested.S.PS2.PB
		{1, 1, 0}, {1, 1, 1}, // Nested.PS.I, Nested.PS.PI
		{1, 1, 2}, {1, 1, 3}, // Nested.PS.CI, Nested.PS.PCI
		{1, 1, 4, 0}, {1, 1, 4, 1}, // Nested.PS.S2.B, Nested.PS.S2.PB
		{1, 1, 5, 0}, {1, 1, 5, 1}, // Nested.PS.PS2.B, Nested.PS.PS2.PB
	}, paths)
	assert.True(t, len(names) == len(paths))

	testStruct := Embedded{}
	for i, path := range paths {
		val, field := followPathThroughStructs(reflect.ValueOf(&testStruct), path)
		assert.True(t, val.IsValid())
		assert.True(t, strings.Contains(names[i][len(names[i])-1], field.Name))
	}
}

func TestCompileQuery(t *testing.T) {
	t.Run("simple struct", func(t *testing.T) {
		type Dest struct {
			Foo  int    `db:"foo"`
			Bar  bool   `db:"bar"`
			Nope string // no tag
		}

		compiled := compileQuery("SELECT $columns FROM greeblies", reflect.TypeOf(Dest{}))
		assert.Equal(t, "SELECT foo, bar FROM greeblies", compiled.query)
	})
	t.Run("complex structs", func(t *testing.T) {
		type CustomInt int
		type S2 struct {
			B  bool  `db:"B"`
			PB *bool `db:"PB"`

			NoTag string
		}
		type S struct {
			I   int        `db:"I"`
			PI  *int       `db:"PI"`
			CI  CustomInt  `db:"CI"`
			PCI *CustomInt `db:"PCI"`
			S2  `db:"S2"`  // embedded!
			PS2 *S2        `db:"PS2"`

			NoTag int
		}
		type Nested struct {
			S  S  `db:"S"`
			PS *S `db:"PS"`

			NoTag S
		}
		type Dest struct {
			NoTag S
			Nested
		}

		compiled := compileQuery("SELECT $columns FROM greeblies", reflect.TypeOf(Dest{}))
		assert.Equal(t, "SELECT S.I, S.PI, S.CI, S.PCI, S_S2.B, S_S2.PB, S_PS2.B, S_PS2.PB, PS.I, PS.PI, PS.CI, PS.PCI, PS_S2.B, PS_S2.PB, PS_PS2.B, PS_PS2.PB FROM greeblies", compiled.query)
	})
	t.Run("int", func(t *testing.T) {
		type Dest int

		// There should be no error here because we do not need to extract columns from
		// the destination type. There may be errors down the line in value iteration, but
		// that is always the case if the Go types don't match the query.
		compiled := compileQuery("SELECT id FROM greeblies", reflect.TypeOf(Dest(0)))
		assert.Equal(t, "SELECT id FROM greeblies", compiled.query)
	})
	t.Run("just one table", func(t *testing.T) {
		type Dest struct {
			Foo  int    `db:"foo"`
			Bar  bool   `db:"bar"`
			Nope string // no tag
		}

		// The prefix is necessary because otherwise we would have to provide a struct with
		// a db tag in order to provide the query with the `greeblies.` prefix in the
		// final query. This comes up a lot when we do a JOIN to help with a condition, but
		// don't actually care about any of the data we joined to.
		compiled := compileQuery(
			"SELECT $columns{greeblies} FROM greeblies NATURAL JOIN props",
			reflect.TypeOf(Dest{}),
		)
		assert.Equal(t, "SELECT greeblies.foo, greeblies.bar FROM greeblies NATURAL JOIN props", compiled.query)
	})

	t.Run("using $columns without a struct is not allowed", func(t *testing.T) {
		type Dest int

		assert.Panics(t, func() {
			compileQuery("SELECT $columns FROM greeblies", reflect.TypeOf(Dest(0)))
		})
	})
}

func TestQueryBuilder(t *testing.T) {
	t.Run("happy time", func(t *testing.T) {
		var qb QueryBuilder
		qb.Add("SELECT stuff FROM thing WHERE foo = $? AND bar = $?", 3, "hello")
		qb.Add("AND (baz = $?)", true)

		assert.Equal(t, "SELECT stuff FROM thing WHERE foo = ? AND bar = ?\nAND (baz = ?)\n", qb.String())
		assert.Equal(t, []interface{}{3, "hello", true}, qb.Args())
	})
	t.Run("too few arguments", func(t *testing.T) {
		var qb QueryBuilder
		assert.Panics(t, func() {
			qb.Add("HELLO $? $? $?", 1, 2)
		})
	})
	t.Run("too many arguments", func(t *testing.T) {
		var qb QueryBuilder
		assert.Panics(t, func() {
			qb.Add("HELLO $? $? $?", 1, 2, 3, 4)
		})
	})
}

func TestSetValueFromDB(t *testing.T) {
	t.Run("ints", func(t *testing.T) {
		t.Run("int to int", func(t *testing.T) {
			var dest int
			var value int = 3

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Equal(t, 3, dest)
		})
		t.Run("int32 to int", func(t *testing.T) {
			var dest int
			var value int32 = 3

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Equal(t, 3, dest)
		})
		t.Run("int to *int", func(t *testing.T) {
			var dest *int
			var value int = 3

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Equal(t, 3, *dest)
		})
		t.Run("int32 to *int", func(t *testing.T) {
			var dest *int
			var value int32 = 3

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Equal(t, 3, *dest)
		})
		t.Run("pointer nil to *int", func(t *testing.T) {
			var dest *int
			var value *int = nil

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Nil(t, dest)
		})
		t.Run("interface nil to *int", func(t *testing.T) {
			var dest *int
			var value interface{} = nil

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Nil(t, dest)
		})
	})

	t.Run("strings", func(t *testing.T) {
		type myString string
		t.Run("string to string", func(t *testing.T) {
			var dest string
			var value string = "handmade"

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Equal(t, "handmade", dest)
		})
		t.Run("custom string to string", func(t *testing.T) {
			var dest string
			var value myString = "handmade"

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Equal(t, "handmade", dest)
		})
		t.Run("string to *string", func(t *testing.T) {
			var dest *string
			var value string = "handmade"

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Equal(t, "handmade", *dest)
		})
		t.Run("custom string to *int", func(t *testing.T) {
			var dest *string
			var value myString = "handmade"

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Equal(t, "handmade", *dest)
		})
		t.Run("pointer nil to *string", func(t *testing.T) {
			var dest *string
			var value *string = nil

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Nil(t, dest)
		})
		t.Run("interface nil to *string", func(t *testing.T) {
			var dest *string
			var value interface{} = nil

			setValueFromDB(reflectPtr(&dest), reflect.ValueOf(value))
			assert.Nil(t, dest)
		})
	})
}

func reflectPtr[T any](dest *T) reflect.Value {
	return reflect.NewAt(reflect.TypeOf(*dest), unsafe.Pointer(dest)).Elem()
}
