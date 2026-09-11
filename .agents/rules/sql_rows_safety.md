# SQL Rows & Database Query Safety Rule

In the Go backend (`sightpane/sightpane`), all database query operations involving `sql.Rows` must adhere strictly to the following query lifecycle and error handling rules to prevent silent data loss, resource leaks, and undetected stream failures.

## 1. Always Check `rows.Err()` After Every `for rows.Next()` Loop

- **Why**: `rows.Next()` returns `false` both when all rows have been read normally (EOF) and when an error occurs during streaming iteration (e.g., network disconnect, timeout, context cancellation, driver decoding failure, or corrupted data stream).
- **Rule**: Every `for rows.Next()` loop MUST be followed immediately by a check of `rows.Err()`.
- **Anti-Pattern**:
  ```go
  // NEVER: Silent data truncation if an iteration error occurs
  for rows.Next() {
      // ...
  }
  return items, nil
  ```
- **Required Pattern**:
  ```go
  for rows.Next() {
      // ...
  }
  if err := rows.Err(); err != nil {
      return nil, fmt.Errorf("iterate ...: %w", err)
  }
  return items, nil
  ```

## 2. Never Swallow `rows.Scan()` Errors

- **Why**: Swallowing scan errors silently drops corrupted, null, or mismatched records from the result set without any indication to the caller.
- **Rule**: Check and return/propagate all `rows.Scan()` errors immediately.
- **Anti-Pattern**:
  ```go
  // NEVER: Silently ignores scanning failures and drops records
  for rows.Next() {
      var id string
      if err := rows.Scan(&id); err == nil {
          items = append(items, id)
      }
  }
  ```
- **Required Pattern**:
  ```go
  for rows.Next() {
      var id string
      if err := rows.Scan(&id); err != nil {
          return nil, fmt.Errorf("scan ...: %w", err)
      }
      items = append(items, id)
  }
  ```

## 3. Never Use `defer rows.Close()` Inside a Loop

- **Why**: In Go, `defer` statements execute only when the enclosing function returns, NOT when the loop block finishes. Placing `defer rows.Close()` inside a loop accumulates unclosed cursors and holds pooled database connections across loop iterations until the entire function exits.
- **Rule**:
  - For standalone queries, `defer rows.Close()` immediately after checking `if err != nil`.
  - Inside loops (e.g., iterating over rules, items, or batches), wrap each query and iteration in a scoped closure (`func() error { ... }()`) with its own `defer rows.Close()`, or close rows explicitly upon loop exit and on every error path.

## 4. Code Auditor Enforcement

Whenever running `/code-auditor`:
- 🔴 **Severity 1 (Critical / Data Loss)**: Any `for rows.Next()` loop without a terminating `if err := rows.Err(); err != nil` check, or any swallowed `rows.Scan` error (`err == nil`).
- 🟠 **Severity 2 (Lifecycle Leak)**: Any `defer rows.Close()` inside a `for` or `range` loop.
