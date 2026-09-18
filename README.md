# go-ownfunc

`go-ownfunc` is a [`golangci-lint` custom linter](https://golangci-lint.run/plugins/module-plugins/) that reports unexported package-level functions whose direct calls all come from methods of one named type. Such a function often belongs on that type as an unexported method instead.

```go
func clearMap(m map[string]string) {
	clear(m)
}

func (c *Cache) Invalidate() { clearMap(c.data) }
func (c *Cache) Reset()      { clearMap(c.data) }
```

The linter reports `clearMap` as a candidate for an unexported `Cache` method. When it can safely rewrite every affected call, it also provides a suggested fix.

## What it checks

`ownfunc` considers unexported, package-level functions. It reports one only when all relevant direct calls are from methods of the same named receiver type; pointer and value receivers of that type count as the same owner.

It does not report functions that are called from a free function, used by methods of different receiver types, exported, or ignored by configuration. By default, taking a candidate as a function value also disqualifies it.

Suggested fixes convert the function into a method and qualify call sites. They handle recursive calls and calls in test files. A diagnostic remains available when the linter cannot safely produce a complete rewrite, such as a method with an unnamed receiver.

## Use with golangci-lint

Build a custom `golangci-lint` binary that includes the module. The plugin build configuration must use the same `golangci-lint` version as the binary.

```yaml
# .custom-gcl.yml
version: v2.13.2

plugins:
  - module: github.com/nfx/go-ownfunc
    path: ./go-ownfunc
```

From the directory containing `.custom-gcl.yml`, build and run the custom binary:

```bash
golangci-lint custom --destination ./bin
./bin/custom-gcl run
```

Enable and configure the linter in `.golangci.yml`:

```yaml
linters:
  enable:
    - ownfunc
  settings:
    custom:
      ownfunc:
        min-calls: 2
        ignore-test-files: true
        allow-function-values: false
        ignored-functions:
          - '^init$'
          - '^new.*$'
        ignored-receiver-types:
          - BaseModel
```

Both kebab-case and snake_case setting names are accepted.

## Configuration

| Setting | Default | Description |
| --- | --- | --- |
| `min-calls` | `1` | Minimum number of relevant method calls before reporting a function. Values less than one use the default. |
| `ignore-test-files` | `true` | Exclude calls in `*_test.go` files from ownership analysis. Test calls are still updated in a suggested fix. |
| `allow-function-values` | `false` | Allow a candidate to be taken as a function value without disqualifying it. |
| `ignored-functions` | `['^init$']` | Regular expressions matched against candidate function names. An empty list uses the default. |
| `ignored-receiver-types` | `[]` | Named receiver types whose method calls should not establish ownership. |

## Standalone analyzer

Run the analyzer directly against packages while developing or testing it:

```bash
go run ./cmd/go-ownfunc ./...
```

The standalone command uses the default configuration. Use the `golangci-lint` plugin to configure the analyzer.
