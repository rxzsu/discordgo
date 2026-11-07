package discordgo

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func BenchmarkLimiterLockParallel(b *testing.B) {
	for _, endpoints := range []int{1, 128} {
		b.Run(strconv.Itoa(endpoints), func(b *testing.B) {
			r := NewRatelimiter()
			keys := make([]string, endpoints)
			for i := range keys {
				keys[i] = "/channels/" + strconv.Itoa(i) + "/messages"
				r.GetBucket(keys[i])
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					bucket := r.LockBucket(keys[i%len(keys)])
					_ = bucket.Release(nil)
					i++
				}
			})
		})
	}
}

func BenchmarkLimiterChurn(b *testing.B) {
	r := NewRatelimiter()
	keys := make([]string, 16384)
	for i := range keys {
		keys[i] = "/channels/" + strconv.Itoa(i) + "/messages"
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bucket := r.LockBucket(keys[i%len(keys)])
		_ = bucket.Release(nil)
	}
}

// BenchmarkBucketReleaseHeaders isolates the Bucket.Release hot path with a
// realistic set of Discord rate-limit response headers.
func BenchmarkBucketReleaseHeaders(b *testing.B) {
	r := NewRatelimiter()
	key := "/channels/1/messages"
	headers := http.Header{}
	headers.Set("X-RateLimit-Remaining", "10")
	headers.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Unix(), 10)+".000")
	headers.Set("Date", time.Now().UTC().Format(http.TimeFormat))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bucket := r.LockBucket(key)
		_ = bucket.Release(headers)
	}
}
