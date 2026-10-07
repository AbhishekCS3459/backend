package availability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBucketFor(t *testing.T) {
	cases := []struct {
		available, threshold int
		want                 Bucket
	}{
		{0, 0, Out},
		{0, 5, Out},
		{1, 0, InStock},
		{1, 3, Low},
		{3, 3, Low},
		{4, 3, InStock},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, BucketFor(c.available, c.threshold), "available=%d threshold=%d", c.available, c.threshold)
	}
}

func TestOrderableQuantity(t *testing.T) {
	assert.Equal(t, 0, OrderableQuantity(-2))
	assert.Equal(t, 0, OrderableQuantity(0))
	assert.Equal(t, 3, OrderableQuantity(3))
	assert.Equal(t, MaxOrderQuantity, OrderableQuantity(MaxOrderQuantity))
	assert.Equal(t, MaxOrderQuantity, OrderableQuantity(500), "never more than the cap")
}

func TestStoreSearchable(t *testing.T) {
	assert.True(t, StoreSearchable("ACTIVE", true, "COMPLETED", false))
	assert.False(t, StoreSearchable("ACTIVE", false, "COMPLETED", false), "closed")
	assert.False(t, StoreSearchable("VACATION", true, "COMPLETED", false), "on vacation")
	assert.False(t, StoreSearchable("INACTIVE", true, "COMPLETED", false), "inactive")
	assert.False(t, StoreSearchable("ACTIVE", true, "DRAFT", false), "setup not finished")
	assert.False(t, StoreSearchable("ACTIVE", true, "COMPLETED", true), "deleted")
}
