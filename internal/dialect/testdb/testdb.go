package testdb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
)

type Response struct {
	ExpectedQuery  string
	Columns        []string
	Rows           [][]driver.Value
	PingError      error
	QueryError     error
	IterationError error
}

type Driver struct {
	mu        sync.Mutex
	responses []Response
	dsn       string
	queries   int
}

func Register(name string) *Driver {
	driverValue := &Driver{}
	sql.Register(name, driverValue)
	return driverValue
}

func (d *Driver) SetResponse(response Response) {
	d.SetResponses(response)
}

// SetResponses installs the ordered query script returned by the next opened
// connection. The first response also controls Ping through PingError.
func (d *Driver) SetResponses(responses ...Response) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.responses = cloneResponses(responses)
	d.dsn = ""
	d.queries = 0
}

func (d *Driver) DSN() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dsn
}

func (d *Driver) Queries() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.queries
}

func (d *Driver) Open(dsn string) (driver.Conn, error) {
	d.mu.Lock()
	d.dsn = dsn
	responses := cloneResponses(d.responses)
	d.mu.Unlock()
	return &connection{driver: d, responses: responses}, nil
}

func cloneResponses(source []Response) []Response {
	result := make([]Response, len(source))
	for index, response := range source {
		result[index] = cloneResponse(response)
	}
	return result
}

func cloneResponse(source Response) Response {
	result := source
	result.Columns = append([]string(nil), source.Columns...)
	result.Rows = make([][]driver.Value, len(source.Rows))
	for index, row := range source.Rows {
		result.Rows[index] = append([]driver.Value(nil), row...)
	}
	return result
}

type connection struct {
	driver    *Driver
	responses []Response
	next      int
}

func (c *connection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare is not supported")
}

func (c *connection) Close() error { return nil }

func (c *connection) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *connection) Ping(context.Context) error {
	if len(c.responses) == 0 {
		return nil
	}
	return c.responses[0].PingError
}

func (c *connection) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.driver.mu.Lock()
	c.driver.queries++
	c.driver.mu.Unlock()
	if c.next >= len(c.responses) {
		return nil, errors.New("unexpected query: script is exhausted")
	}
	response := c.responses[c.next]
	c.next++
	if response.QueryError != nil {
		return nil, response.QueryError
	}
	if query != response.ExpectedQuery {
		return nil, errors.New("unexpected query")
	}
	return &rows{
		columns:        response.Columns,
		values:         response.Rows,
		iterationError: response.IterationError,
	}, nil
}

type rows struct {
	columns        []string
	values         [][]driver.Value
	index          int
	iterationError error
}

func (r *rows) Columns() []string { return r.columns }

func (r *rows) Close() error { return nil }

func (r *rows) Next(destination []driver.Value) error {
	if r.index < len(r.values) {
		copy(destination, r.values[r.index])
		r.index++
		return nil
	}
	if r.iterationError != nil {
		err := r.iterationError
		r.iterationError = nil
		return err
	}
	return io.EOF
}
