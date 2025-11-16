# memory

In-memory metric storage. Data is lost when the process stops.

## When to use

- **Testing**: Fast, no disk I/O, easy cleanup
- **Development**: No setup required
- **Short-lived processes**: Data doesn't need to persist

## When NOT to use

- Production (use badger instead)
- Long retention periods
- Restartability required

## Usage

```go
import "tinyobs/pkg/storage/memory"

store := memory.New()
defer store.Close()

// That's it - zero config needed
```

## Limitations

- Unbounded memory growth (no automatic cleanup)
- Single-threaded queries (RWMutex)
- Linear scan for queries (no indexing)

Good enough for local dev, not for production.
