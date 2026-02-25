package tools

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/microsoft/go-mssqldb"
)

func mssqlServer() *builtinMCPServer {
	return &builtinMCPServer{
		tools: map[string]ToolDef{
			"list_tables": {
				Description: "List all tables in the connected SQL Server database, showing schema and table name.",
				Fn:          ToolFunc(mssqlListTables),
				Params:      map[string]ParamDef{},
			},
			"describe_table": {
				Description: "Show columns, data types, nullability, and key constraints for a given table.",
				Fn:          ToolFunc(mssqlDescribeTable),
				Params: map[string]ParamDef{
					"table": {
						Type:        "string",
						Description: "Table name, optionally schema-qualified (e.g. 'dbo.Users')",
						Required:    true,
					},
				},
			},
			"execute_query": {
				Description: "Run a read-only SQL query (SELECT) and return results as formatted text. Use this for reading data.",
				Fn:          ToolFunc(mssqlExecuteQuery),
				Params: map[string]ParamDef{
					"query": {
						Type:        "string",
						Description: "SQL SELECT query to execute",
						Required:    true,
					},
				},
			},
			"execute_statement": {
				Description: "Run a write SQL statement (INSERT, UPDATE, DELETE, CREATE, ALTER, DROP) and return rows affected.",
				Fn:          ToolFunc(mssqlExecuteStatement),
				Params: map[string]ParamDef{
					"statement": {
						Type:        "string",
						Description: "SQL statement to execute",
						Required:    true,
					},
				},
			},
		},
	}
}

// mssqlConnect builds a connection string from environment variables and returns a *sql.DB.
func mssqlConnect() (*sql.DB, error) {
	server := os.Getenv("SERVER_NAME")
	database := os.Getenv("DATABASE_NAME")
	user := os.Getenv("SQL_USERNAME")
	password := os.Getenv("SQL_PASSWORD")

	if server == "" || database == "" || user == "" || password == "" {
		return nil, fmt.Errorf("missing required env vars: SERVER_NAME, DATABASE_NAME, SQL_USERNAME, SQL_PASSWORD")
	}

	port := os.Getenv("SQL_PORT")
	if port == "" {
		port = "1433"
	}

	trustCert := os.Getenv("TRUST_SERVER_CERTIFICATE")

	connStr := fmt.Sprintf("sqlserver://%s:%s@%s:%s?database=%s",
		user, password, server, port, database)

	if strings.EqualFold(trustCert, "true") || trustCert == "1" {
		connStr += "&TrustServerCertificate=true"
	}

	db, err := sql.Open("sqlserver", connStr)
	if err != nil {
		return nil, fmt.Errorf("open connection: %w", err)
	}

	return db, nil
}

func mssqlListTables(ctx context.Context, params map[string]any) (string, error) {
	db, err := mssqlConnect()
	if err != nil {
		return "", err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT TABLE_SCHEMA, TABLE_NAME
		 FROM INFORMATION_SCHEMA.TABLES
		 WHERE TABLE_TYPE = 'BASE TABLE'
		 ORDER BY TABLE_SCHEMA, TABLE_NAME`)
	if err != nil {
		return "", fmt.Errorf("query tables: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("Schema | Table\n")
	sb.WriteString("-------|------\n")

	count := 0
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return "", fmt.Errorf("scan row: %w", err)
		}
		sb.WriteString(schema)
		sb.WriteString(" | ")
		sb.WriteString(name)
		sb.WriteByte('\n')
		count++
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate rows: %w", err)
	}

	sb.WriteString(fmt.Sprintf("\n%d table(s) found.", count))
	return sb.String(), nil
}

func mssqlDescribeTable(ctx context.Context, params map[string]any) (string, error) {
	table, _ := params["table"].(string)
	if table == "" {
		return "", fmt.Errorf("table parameter is required")
	}

	db, err := mssqlConnect()
	if err != nil {
		return "", err
	}
	defer db.Close()

	// Split schema.table if provided.
	schema := "dbo"
	tableName := table
	if parts := strings.SplitN(table, ".", 2); len(parts) == 2 {
		schema = parts[0]
		tableName = parts[1]
	}

	rows, err := db.QueryContext(ctx,
		`SELECT
			c.COLUMN_NAME,
			c.DATA_TYPE,
			c.CHARACTER_MAXIMUM_LENGTH,
			c.IS_NULLABLE,
			CASE WHEN kcu.COLUMN_NAME IS NOT NULL THEN 'PK' ELSE '' END AS KEY_TYPE
		 FROM INFORMATION_SCHEMA.COLUMNS c
		 LEFT JOIN INFORMATION_SCHEMA.TABLE_CONSTRAINTS tc
			ON tc.TABLE_SCHEMA = c.TABLE_SCHEMA
			AND tc.TABLE_NAME = c.TABLE_NAME
			AND tc.CONSTRAINT_TYPE = 'PRIMARY KEY'
		 LEFT JOIN INFORMATION_SCHEMA.KEY_COLUMN_USAGE kcu
			ON kcu.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
			AND kcu.TABLE_SCHEMA = tc.TABLE_SCHEMA
			AND kcu.COLUMN_NAME = c.COLUMN_NAME
		 WHERE c.TABLE_SCHEMA = @p1 AND c.TABLE_NAME = @p2
		 ORDER BY c.ORDINAL_POSITION`,
		schema, tableName)
	if err != nil {
		return "", fmt.Errorf("query columns: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Table: %s.%s\n\n", schema, tableName))
	sb.WriteString("Column | Type | Nullable | Key\n")
	sb.WriteString("-------|------|----------|----\n")

	count := 0
	for rows.Next() {
		var colName, dataType, nullable, keyType string
		var maxLen *int
		if err := rows.Scan(&colName, &dataType, &maxLen, &nullable, &keyType); err != nil {
			return "", fmt.Errorf("scan row: %w", err)
		}

		typeStr := dataType
		if maxLen != nil && *maxLen > 0 {
			typeStr = fmt.Sprintf("%s(%d)", dataType, *maxLen)
		}

		sb.WriteString(colName)
		sb.WriteString(" | ")
		sb.WriteString(typeStr)
		sb.WriteString(" | ")
		sb.WriteString(nullable)
		sb.WriteString(" | ")
		sb.WriteString(keyType)
		sb.WriteByte('\n')
		count++
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate rows: %w", err)
	}

	if count == 0 {
		return fmt.Sprintf("Table %s.%s not found or has no columns.", schema, tableName), nil
	}

	return sb.String(), nil
}

func mssqlExecuteQuery(ctx context.Context, params map[string]any) (string, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	// Basic safety check: only allow SELECT-like statements.
	trimmed := strings.TrimSpace(strings.ToUpper(query))
	if !strings.HasPrefix(trimmed, "SELECT") && !strings.HasPrefix(trimmed, "WITH") {
		return "", fmt.Errorf("execute_query only supports SELECT/WITH statements; use execute_statement for write operations")
	}

	db, err := mssqlConnect()
	if err != nil {
		return "", err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return "", fmt.Errorf("execute query: %w", err)
	}
	defer rows.Close()

	return formatRows(rows)
}

func mssqlExecuteStatement(ctx context.Context, params map[string]any) (string, error) {
	stmt, _ := params["statement"].(string)
	if stmt == "" {
		return "", fmt.Errorf("statement parameter is required")
	}

	db, err := mssqlConnect()
	if err != nil {
		return "", err
	}
	defer db.Close()

	result, err := db.ExecContext(ctx, stmt)
	if err != nil {
		return "", fmt.Errorf("execute statement: %w", err)
	}

	affected, _ := result.RowsAffected()
	return fmt.Sprintf("%d row(s) affected.", affected), nil
}

// formatRows converts sql.Rows into a pipe-delimited text table.
func formatRows(rows *sql.Rows) (string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("get columns: %w", err)
	}

	var sb strings.Builder

	// Header.
	sb.WriteString(strings.Join(cols, " | "))
	sb.WriteByte('\n')
	for i, c := range cols {
		if i > 0 {
			sb.WriteString("-|-")
		}
		sb.WriteString(strings.Repeat("-", len(c)))
	}
	sb.WriteByte('\n')

	// Rows.
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}

	count := 0
	const maxRows = 500
	for rows.Next() {
		if count >= maxRows {
			sb.WriteString(fmt.Sprintf("\n... truncated at %d rows.", maxRows))
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", fmt.Errorf("scan row: %w", err)
		}
		for i, v := range values {
			if i > 0 {
				sb.WriteString(" | ")
			}
			sb.WriteString(formatValue(v))
		}
		sb.WriteByte('\n')
		count++
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate rows: %w", err)
	}

	sb.WriteString(fmt.Sprintf("\n%d row(s) returned.", count))
	return sb.String(), nil
}

// formatValue converts a database value to a display string.
func formatValue(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case []byte:
		return string(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}
