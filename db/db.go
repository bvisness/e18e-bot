package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/bvisness/e18e-bot/config"
	"github.com/bvisness/e18e-bot/utils"
)

/*
A general error to be used when no results are found. This is the error returned
by QueryOne, and can generally be used by other database helpers that fetch a single
result but find nothing.
*/
var NotFound = errors.New("not found")

// This interface should match both a direct pgx connection or a pgx transaction.
type ConnOrTx interface {
	QueryContext(ctx context.Context, sql string, args ...any) (*sql.Rows, error)
	// QueryRow(ctx context.Context, sql string, args ...any) sql.Row
	ExecContext(ctx context.Context, sql string, args ...any) (sql.Result, error)
	// CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)

	// // Both raw database connections and transactions in pgx can begin/commit
	// // transactions. For database connections it does the obvious thing; for
	// // transactions it creates a "pseudo-nested transaction" but conceptually
	// // works the same. See the documentation of pgx.Tx.Begin.
	// Begin(ctx context.Context) (pgx.Tx, error)
}

func Open() *sql.DB {
	return utils.Must1(sql.Open("sqlite3", config.Config.Db.DSN))
}

/*
Performs a SQL query and returns a slice of all the result rows. The query is just plain SQL, but make sure to read the package documentation for details. You must explicitly provide the type argument - this is how it knows what Go type to map the results to, and it cannot be inferred.

Any SQL query may be performed, including INSERT and UPDATE - as long as it returns a result set, you can use this. If the query does not return a result set, or you simply do not care about the result set, call Exec directly on your pgx connection.

This function always returns pointers to the values. This is convenient for structs, but for other types, you may wish to use QueryScalar.
*/
func Query[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) ([]*T, error) {
	it, err := QueryIterator[T](ctx, conn, query, args...)
	if err != nil {
		return nil, err
	} else {
		return it.ToSlice(), nil
	}
}

/*
Identical to Query, but panics if there was an error.
*/
func MustQuery[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) []*T {
	result, err := Query[T](ctx, conn, query, args...)
	if err != nil {
		panic(err)
	}
	return result
}

/*
Identical to Query, but returns only the first result row. If there are no
rows in the result set, returns NotFound.
*/
func QueryOne[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) (*T, error) {
	rows, err := QueryIterator[T](ctx, conn, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result, hasRow := rows.Next()
	if !hasRow {
		if readErr := rows.Err(); readErr != nil {
			return nil, readErr
		} else {
			return nil, NotFound
		}
	}

	return result, nil
}

/*
Identical to QueryOne, but panics if there was an error.
*/
func MustQueryOne[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) *T {
	result, err := QueryOne[T](ctx, conn, query, args...)
	if err != nil {
		panic(err)
	}
	return result
}

/*
Identical to Query, but returns concrete values instead of pointers. More convenient
for primitive types.
*/
func QueryScalar[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) ([]T, error) {
	rows, err := QueryIterator[T](ctx, conn, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []T
	for {
		val, hasRow := rows.Next()
		if !hasRow {
			break
		}
		result = append(result, *val)
	}

	return result, nil
}

/*
Identical to QueryScalar, but panics if there was an error.
*/
func MustQueryScalar[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) []T {
	result, err := QueryScalar[T](ctx, conn, query, args...)
	if err != nil {
		panic(err)
	}
	return result
}

/*
Identical to QueryScalar, but returns only the first result value. If there are
no rows in the result set, returns NotFound.
*/
func QueryOneScalar[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) (T, error) {
	rows, err := QueryIterator[T](ctx, conn, query, args...)
	if err != nil {
		var zero T
		return zero, err
	}
	defer rows.Close()

	result, hasRow := rows.Next()
	if !hasRow {
		var zero T
		if readErr := rows.Err(); readErr != nil {
			return zero, readErr
		} else {
			return zero, NotFound
		}
	}

	return *result, nil
}

/*
Identical to QueryOneScalar, but panics if there was an error.
*/
func MustQueryOneScalar[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) T {
	result, err := QueryOneScalar[T](ctx, conn, query, args...)
	if err != nil {
		panic(err)
	}
	return result
}

// A passthrough to the Exec method, because it's annoying to have to backspace "db."
func Exec(ctx context.Context, conn ConnOrTx, query string, args ...any) (sql.Result, error) {
	return conn.ExecContext(ctx, query, args...)
}

/*
Identical to Query, but returns the ResultIterator instead of automatically converting the results to a slice. The iterator must be closed after use.
*/
func QueryIterator[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) (*Iterator[T], error) {
	var destExample T
	destType := reflect.TypeOf(destExample)

	compiled := compileQuery(query, destType)

	rows, err := conn.QueryContext(ctx, compiled.query, args...)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			panic("query exceeded its deadline")
		}
		return nil, err
	}

	it := &Iterator[T]{
		fieldPaths:       compiled.fieldPaths,
		rows:             rows,
		destType:         compiled.destType,
		destTypeIsScalar: typeIsQueryable(compiled.destType),
		closed:           make(chan struct{}, 1),
	}

	// Ensure that iterators are closed if context is cancelled. Otherwise, iterators can hold
	// open connections even after a request is cancelled, causing the app to deadlock.
	go func() {
		done := ctx.Done()
		if done == nil {
			return
		}
		select {
		case <-done:
			it.Close()
		case <-it.closed:
		}
	}()

	return it, nil
}

/*
Identical to QueryIterator, but panics if there was an error.
*/
func MustQueryIterator[T any](
	ctx context.Context,
	conn ConnOrTx,
	query string,
	args ...any,
) *Iterator[T] {
	result, err := QueryIterator[T](ctx, conn, query, args...)
	if err != nil {
		panic(err)
	}
	return result
}

// TODO: QueryFunc?

type compiledQuery struct {
	query      string
	destType   reflect.Type
	fieldPaths []fieldPath
}

var reColumnsPlaceholder = regexp.MustCompile(`\$columns({(.*?)})?`)

func compileQuery(query string, destType reflect.Type) compiledQuery {
	columnsMatch := reColumnsPlaceholder.FindStringSubmatch(query)
	hasColumnsPlaceholder := columnsMatch != nil

	if hasColumnsPlaceholder {
		// The presence of the $columns placeholder means that the destination type
		// must be a struct, and we will plonk that struct's fields into the query.

		if destType.Kind() != reflect.Struct {
			panic("$columns can only be used when querying into a struct")
		}

		var prefix []string
		prefixText := columnsMatch[2]
		if prefixText != "" {
			prefix = []string{prefixText}
		}

		columnNames, fieldPaths := getColumnNamesAndPaths(destType, nil, prefix)

		columns := make([]string, 0, len(columnNames))
		for _, strSlice := range columnNames {
			tableName := strings.Join(strSlice[0:len(strSlice)-1], "_")
			fullName := strSlice[len(strSlice)-1]
			if tableName != "" {
				fullName = tableName + "." + fullName
			}
			columns = append(columns, fullName)
		}

		columnNamesString := strings.Join(columns, ", ")
		query = reColumnsPlaceholder.ReplaceAllString(query, columnNamesString)

		return compiledQuery{
			query:      query,
			destType:   destType,
			fieldPaths: fieldPaths,
		}
	} else {
		return compiledQuery{
			query:    query,
			destType: destType,
		}
	}
}

func getColumnNamesAndPaths(destType reflect.Type, pathSoFar []int, prefix []string) (names []columnName, paths []fieldPath) {
	var columnNames []columnName
	var fieldPaths []fieldPath

	if destType.Kind() == reflect.Ptr {
		destType = destType.Elem()
	}

	if destType.Kind() != reflect.Struct {
		panic(fmt.Errorf("can only get column names and paths from a struct, got type '%v' (at prefix '%v')", destType.Name(), prefix))
	}

	type AnonPrefix struct {
		Path   []int
		Prefix string
	}
	var anonPrefixes []AnonPrefix

	for _, field := range reflect.VisibleFields(destType) {
		path := make([]int, len(pathSoFar))
		copy(path, pathSoFar)
		path = append(path, field.Index...)
		fieldColumnNames := prefix[:]

		if columnName := field.Tag.Get("db"); columnName != "" {
			if field.Anonymous {
				anonPrefixes = append(anonPrefixes, AnonPrefix{Path: field.Index, Prefix: columnName})
				continue
			} else {
				for _, anonPrefix := range anonPrefixes {
					if len(field.Index) > len(anonPrefix.Path) {
						equal := true
						for i := range anonPrefix.Path {
							if anonPrefix.Path[i] != field.Index[i] {
								equal = false
								break
							}
						}
						if equal {
							fieldColumnNames = append(fieldColumnNames, anonPrefix.Prefix)
							break
						}
					}
				}
			}
			fieldType := field.Type
			if fieldType.Kind() == reflect.Ptr {
				fieldType = fieldType.Elem()
			}

			fieldColumnNames = append(fieldColumnNames, columnName)

			if typeIsQueryable(fieldType) {
				columnNames = append(columnNames, fieldColumnNames)
				fieldPaths = append(fieldPaths, path)
			} else if fieldType.Kind() == reflect.Struct {
				subCols, subPaths := getColumnNamesAndPaths(fieldType, path, fieldColumnNames)
				columnNames = append(columnNames, subCols...)
				fieldPaths = append(fieldPaths, subPaths...)
			} else {
				panic(fmt.Errorf("field '%s' in type %s has invalid type '%s'", field.Name, destType, field.Type))
			}
		}
	}

	return columnNames, fieldPaths
}

/*
Checks if we are able to handle a particular type in a database query. This applies only to
primitive types and not structs, since the database only returns individual primitive types
and it is our job to stitch them back together into structs later.
*/
func typeIsQueryable(t reflect.Type) bool {
	if reflect.PointerTo(t).Implements(reflect.TypeOf((*sql.Scanner)(nil)).Elem()) {
		return true
	}

	k := t.Kind()
	if k == reflect.Slice && t.Elem() == reflect.TypeOf(byte(0)) {
		return true // []byte
	} else if t == reflect.TypeOf(time.Time{}) {
		return true // time.Time
	}

	var queryableKinds = []reflect.Kind{
		reflect.Int, reflect.Int64,
		reflect.Uint, reflect.Uint64,
		reflect.Float64,
		reflect.Bool,
		// []byte handled above
		reflect.String,
		// time.Time handled above
	}

	for _, qk := range queryableKinds {
		if k == qk {
			return true
		}
	}

	return false
}

type columnName []string

// A path to a particular field in query's destination type. Each index in the slice
// corresponds to a field index for use with Field on a reflect.Type or reflect.Value.
type fieldPath []int

type Iterator[T any] struct {
	fieldPaths       []fieldPath
	rows             *sql.Rows
	destType         reflect.Type
	destTypeIsScalar bool // NOTE(ben): Make sure this gets set every time destType gets set, based on typeIsQueryable(destType). This is kinda fragile...but also contained to this file, so doesn't seem worth a lazy evaluation or a constructor function.
	closed           chan struct{}
}

func (it *Iterator[T]) Next() (*T, bool) {
	// TODO(ben): What happens if this panics? Does it leak resources? Do we need
	// to put a recover() here and close the rows?

	hasNext := it.rows.Next()
	if !hasNext {
		it.Close()
		return nil, false
	}

	cols := utils.Must1(it.rows.ColumnTypes())
	result := reflect.New(it.destType)
	resultConcrete := result.Interface().(*T)

	if it.destTypeIsScalar {
		// This type can be directly queried, meaning it's a simple scalar
		// thing, and we can just take the easy way out.
		if len(cols) != 1 {
			panic(fmt.Errorf("tried to query a scalar value, but got %v values in the row", len(cols)))
		}
		utils.Must(it.rows.Scan(resultConcrete))
	} else {
		vals := make([]any, len(cols))
		for i := range vals {
			fieldValue, _ := followPathThroughStructs(result, it.fieldPaths[i])
			vals[i] = fieldValue.Addr().Interface()
		}
		utils.Must(it.rows.Scan(vals...))
	}
	return resultConcrete, true
}

func (it *Iterator[T]) Err() error {
	return it.rows.Err()
}

// Takes a value from a database query (reflected) and assigns it to the
// destination. If the destination is a pointer, and the value is non-nil, it
// will initialize the destination before assigning.
func setValueFromDB(dest reflect.Value, value reflect.Value) {
	switch v := value.Interface().(type) {
	case sql.NullBool:
		if v.Valid {
			writableDest(dest).SetBool(v.Bool)
		}
	case sql.NullByte:
		if v.Valid {
			writableDest(dest).SetUint(uint64(v.Byte))
		}
	case sql.NullFloat64:
		if v.Valid {
			writableDest(dest).SetFloat(v.Float64)
		}
	case sql.NullInt16:
		if v.Valid {
			setIntish(writableDest(dest), int64(v.Int16))
		}
	case sql.NullInt32:
		if v.Valid {
			setIntish(writableDest(dest), int64(v.Int32))
		}
	case sql.NullInt64:
		if v.Valid {
			setIntish(writableDest(dest), v.Int64)
		}
	case sql.NullString:
		if v.Valid {
			writableDest(dest).SetString(v.String)
		}
	// sql.NullTime is not handled here
	default:
		if dest.Kind() == reflect.Pointer {
			valueIsNilPointer := value.Kind() == reflect.Ptr && value.IsNil()
			if !value.IsValid() || valueIsNilPointer {
				dest.Set(reflect.Zero(dest.Type())) // nil to nil, the end
			} else {
				dest.Set(reflect.New(dest.Type().Elem()))
				dest.Elem().Set(value)
			}
		} else {
			// TODO: This is unlikely to actually work, I think?
			dest.Set(value)
		}
	}

	// TODO: Resurrect this old slice code if SQLite needs it?
	// case reflect.Slice:
	// 	valLen := value.Len()
	// 	if valLen > 0 {
	// 		destTemp := reflect.MakeSlice(dest.Type(), 0, valLen)
	// 		for i := 0; i < valLen; i++ {
	// 			destTemp = reflect.Append(destTemp, value.Index(i).Elem())
	// 		}
	// 		dest.Set(destTemp)
	// 	}
}

// Gets a writeable destination for a value from the database, allocating
// if necessary (specifically if the destination is a pointer type).
func writableDest(dest reflect.Value) reflect.Value {
	if dest.Kind() == reflect.Pointer {
		dest.Set(reflect.New(dest.Type().Elem()))
		dest = dest.Elem()
	}
	return dest
}

func setIntish(dest reflect.Value, value int64) {
	if dest.Kind() == reflect.Bool {
		dest.SetBool(value != 0)
	} else {
		dest.SetInt(value)
	}
}

func (it *Iterator[any]) Close() {
	it.rows.Close()
	select {
	case it.closed <- struct{}{}:
	default:
	}
}

/*
Pulls all the remaining values into a slice, and closes the iterator.
*/
func (it *Iterator[T]) ToSlice() []*T {
	defer it.Close()
	var result []*T
	for {
		row, ok := it.Next()
		if !ok {
			err := it.rows.Err()
			if err != nil {
				panic(fmt.Errorf("error while iterating through db results: %w", err))
			}
			break
		}
		result = append(result, row)
	}
	return result
}

func followPathThroughStructs(structPtrVal reflect.Value, path []int) (reflect.Value, reflect.StructField) {
	if len(path) < 1 {
		panic(fmt.Errorf("can't follow an empty path"))
	}

	if structPtrVal.Kind() != reflect.Ptr || structPtrVal.Elem().Kind() != reflect.Struct {
		panic(fmt.Errorf("structPtrVal must be a pointer to a struct; got value of type %s", structPtrVal.Type()))
	}

	// more informative panic recovery
	var field reflect.StructField
	defer func() {
		if r := recover(); r != nil {
			panic(fmt.Errorf("panic at field '%s': %v", field.Name, r))
		}
	}()

	val := structPtrVal
	for _, i := range path {
		if val.Kind() == reflect.Ptr && val.Type().Elem().Kind() == reflect.Struct {
			if val.IsNil() {
				val.Set(reflect.New(val.Type().Elem()))
			}
			val = val.Elem()
		}
		field = val.Type().Field(i)
		val = val.Field(i)
	}
	return val, field
}
