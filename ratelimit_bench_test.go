package discordgo

import (
	"strconv"
	"testing"
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
