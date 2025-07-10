package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// BenchmarkBadgerStore benchmarks Badger store operations
func BenchmarkBadgerStore(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "badger-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(tempDir)
	
	config := BadgerConfig{
		Path:         tempDir,
		SyncWrites:   false,
		Compression:  true,
		NumVersions:  1,
		NumLevelZero: 5,
		MemTableSize: 64 << 20, // 64MB
	}
	
	store, err := OpenBadger(config)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	
	ctx := context.Background()
	
	b.Run("Set", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("bench-key-%d", i)
			value := []byte(fmt.Sprintf("bench-value-%d", i))
			if err := store.Set(ctx, key, value); err != nil {
				b.Fatal(err)
			}
		}
	})
	
	// Prepare data for read benchmarks
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("read-key-%d", i)
		value := []byte(fmt.Sprintf("read-value-%d", i))
		if err := store.Set(ctx, key, value); err != nil {
			b.Fatal(err)
		}
	}
	
	b.Run("Get", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("read-key-%d", i%1000)
			if _, err := store.Get(ctx, key); err != nil {
				b.Fatal(err)
			}
		}
	})
	
	b.Run("Exists", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("read-key-%d", i%1000)
			if _, err := store.Exists(ctx, key); err != nil {
				b.Fatal(err)
			}
		}
	})
	
	b.Run("Delete", func(b *testing.B) {
		// Prepare keys to delete
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("delete-key-%d", i)
			value := []byte("value")
			if err := store.Set(ctx, key, value); err != nil {
				b.Fatal(err)
			}
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("delete-key-%d", i)
			if err := store.Delete(ctx, key); err != nil {
				b.Fatal(err)
			}
		}
	})
	
	b.Run("BatchSet", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			items := make(map[string][]byte)
			for j := 0; j < 10; j++ {
				key := fmt.Sprintf("batch-key-%d-%d", i, j)
				value := []byte(fmt.Sprintf("batch-value-%d-%d", i, j))
				items[key] = value
			}
			if err := store.BatchSet(ctx, items); err != nil {
				b.Fatal(err)
			}
		}
	})
	
	b.Run("SetWithTTL", func(b *testing.B) {
		ttl := 5 * time.Minute
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("ttl-key-%d", i)
			value := []byte(fmt.Sprintf("ttl-value-%d", i))
			if err := store.SetWithTTL(ctx, key, value, ttl); err != nil {
				b.Fatal(err)
			}
		}
	})
	
	b.Run("Increment", func(b *testing.B) {
		key := "counter-key"
		if err := store.Set(ctx, key, SerializeInt64(0)); err != nil {
			b.Fatal(err)
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := store.Increment(ctx, key, 1); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkMockStore benchmarks mock store operations for comparison
func BenchmarkMockStore(b *testing.B) {
	store := NewMockStore()
	defer store.Close()
	
	ctx := context.Background()
	
	b.Run("Set", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("bench-key-%d", i)
			value := []byte(fmt.Sprintf("bench-value-%d", i))
			if err := store.Set(ctx, key, value); err != nil {
				b.Fatal(err)
			}
		}
	})
	
	// Prepare data for read benchmarks
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("read-key-%d", i)
		value := []byte(fmt.Sprintf("read-value-%d", i))
		if err := store.Set(ctx, key, value); err != nil {
			b.Fatal(err)
		}
	}
	
	b.Run("Get", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("read-key-%d", i%1000)
			if _, err := store.Get(ctx, key); err != nil {
				b.Fatal(err)
			}
		}
	})
	
	b.Run("ConcurrentGet", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := fmt.Sprintf("read-key-%d", i%1000)
				if _, err := store.Get(ctx, key); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})
	})
	
	b.Run("ConcurrentSet", func(b *testing.B) {
		var counter int64
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				id := counter
				counter++
				key := fmt.Sprintf("concurrent-key-%d", id)
				value := []byte(fmt.Sprintf("concurrent-value-%d", id))
				if err := store.Set(ctx, key, value); err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}

// BenchmarkStorageSerialization benchmarks serialization helpers
func BenchmarkStorageSerialization(b *testing.B) {
	b.Run("SerializeInt64", func(b *testing.B) {
		value := int64(1234567890)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = SerializeInt64(value)
		}
	})
	
	b.Run("DeserializeInt64", func(b *testing.B) {
		data := SerializeInt64(1234567890)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := DeserializeInt64(data)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	
	b.Run("SerializeString", func(b *testing.B) {
		value := "benchmark-test-string-value"
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = SerializeString(value)
		}
	})
	
	b.Run("DeserializeString", func(b *testing.B) {
		data := SerializeString("benchmark-test-string-value")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = DeserializeString(data)
		}
	})
	
}

// BenchmarkConcurrentOperations benchmarks concurrent access patterns
func BenchmarkConcurrentOperations(b *testing.B) {
	store := NewMockStore()
	defer store.Close()
	
	ctx := context.Background()
	
	// Pre-populate store
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := []byte(fmt.Sprintf("value-%d", i))
		if err := store.Set(ctx, key, value); err != nil {
			b.Fatal(err)
		}
	}
	
	b.Run("MixedReadWrite", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				if i%3 == 0 {
					// Write
					key := fmt.Sprintf("mixed-key-%d", i)
					value := []byte(fmt.Sprintf("mixed-value-%d", i))
					store.Set(ctx, key, value)
				} else {
					// Read
					key := fmt.Sprintf("key-%d", i%100)
					store.Get(ctx, key)
				}
				i++
			}
		})
	})
	
	b.Run("HighContention", func(b *testing.B) {
		// All goroutines access the same set of keys
		hotKeys := []string{"hot-1", "hot-2", "hot-3", "hot-4", "hot-5"}
		for _, key := range hotKeys {
			store.Set(ctx, key, []byte("initial"))
		}
		
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := hotKeys[i%len(hotKeys)]
				if i%2 == 0 {
					store.Get(ctx, key)
				} else {
					value := []byte(fmt.Sprintf("update-%d", i))
					store.Set(ctx, key, value)
				}
				i++
			}
		})
	})
}