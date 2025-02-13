package cache

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"go.uber.org/atomic"
)

type Item struct {
	Object     interface{}
	Expiration int64
}

// Returns true if the item has expired.
func (item Item) Expired() bool {
	if item.Expiration == 0 {
		return false
	}
	return time.Now().UnixNano() > item.Expiration
}

const (
	// For use with functions that take an expiration time.
	NoExpiration time.Duration = -1
	// For use with functions that take an expiration time. Equivalent to
	// passing in the same expiration duration as was given to New() or
	// NewFrom() when the cache was created (e.g. 5 minutes.)
	DefaultExpiration time.Duration = 0
)

type Cache struct {
	*cache
	// If this is confusing, see the comment at the bottom of New()
}

type cache struct {
	defaultExpiration time.Duration
	items             sync.Map
	counter           atomic.Uint32
	onEvicted         func(string, interface{})
	janitor           *janitor
}

func (c *cache) safeStore(key, value interface{}) {
	c.items.Store(key, value)
	c.counter.Inc()
}
func (c *cache) safeDelete(key interface{}) {
	c.items.Delete(key)
	c.counter.Dec()
}

// Add an item to the cache, replacing any existing item. If the duration is 0
// (DefaultExpiration), the cache's default expiration time is used. If it is -1
// (NoExpiration), the item never expires.
func (c *cache) Set(k string, x interface{}, d time.Duration) {
	// "Inlining" of set
	var e int64
	if d == DefaultExpiration {
		d = c.defaultExpiration
	}
	if d > 0 {
		e = time.Now().Add(d).UnixNano()
	}

	c.safeStore(k, Item{
		Object:     x,
		Expiration: e,
	})

}

func (c *cache) set(k string, x interface{}, d time.Duration) {
	var e int64
	if d == DefaultExpiration {
		d = c.defaultExpiration
	}
	if d > 0 {
		e = time.Now().Add(d).UnixNano()
	}
	c.safeStore(k, Item{
		Object:     x,
		Expiration: e,
	})

}

// Add an item to the cache, replacing any existing item, using the default
// expiration.
func (c *cache) SetDefault(k string, x interface{}) {
	c.Set(k, x, DefaultExpiration)
}

// Add an item to the cache only if an item doesn't already exist for the given
// key, or if the existing item has expired. Returns an error otherwise.
func (c *cache) Add(k string, x interface{}, d time.Duration) error {

	_, found := c.get(k)
	if found {

		return fmt.Errorf("Item %s already exists", k)
	}
	c.set(k, x, d)

	return nil
}

// Set a new value for the cache key only if it already exists, and the existing
// item hasn't expired. Returns an error otherwise.
func (c *cache) Replace(k string, x interface{}, d time.Duration) error {

	_, found := c.get(k)
	if !found {

		return fmt.Errorf("Item %s doesn't exist", k)
	}
	c.set(k, x, d)

	return nil
}

// Get an item from the cache. Returns the item or nil, and a bool indicating
// whether the key was found.
func (c *cache) Get(k string) (interface{}, bool) {

	// "Inlining" of get and Expired
	item, found := c.items.Load(k)

	if !found {

		return nil, false
	}

	if item.(Item).Expiration > 0 {
		if time.Now().UnixNano() > item.(Item).Expiration {

			return nil, false
		}
	}

	return item.(Item).Object, true
}

// GetWithExpiration returns an item and its expiration time from the cache.
// It returns the item or nil, the expiration time if one is set (if the item
// never expires a zero value for time.Time is returned), and a bool indicating
// whether the key was found.
func (c *cache) GetWithExpiration(k string) (interface{}, time.Time, bool) {

	// "Inlining" of get and Expired
	item, found := c.items.Load(k)
	if !found {

		return nil, time.Time{}, false
	}

	if item.(Item).Expiration > 0 {
		if time.Now().UnixNano() > item.(Item).Expiration {

			return nil, time.Time{}, false
		}

		// Return the item and the expiration time

		return item.(Item).Object, time.Unix(0, item.(Item).Expiration), true
	}

	// If expiration <= 0 (i.e. no expiration time set) then return the item
	// and a zeroed time.Time

	return item.(Item).Object, time.Time{}, true
}

func (c *cache) get(k string) (interface{}, bool) {
	item, found := c.items.Load(k)
	if !found {
		return nil, false
	}
	// "Inlining" of Expired
	if item.(Item).Expiration > 0 {
		if time.Now().UnixNano() > item.(Item).Expiration {
			return nil, false
		}
	}
	return item.(Item).Object, true
}

// Increment an item of type int, int8, int16, int32, int64, uintptr, uint,
// uint8, uint32, or uint64, float32 or float64 by n. Returns an error if the
// item's value is not an integer, if it was not found, or if it is not
// possible to increment it by n. To retrieve the incremented value, use one
// of the specialized methods, e.g. IncrementInt64.
func (c *cache) Increment(k string, n int64) error {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	switch v.Object.(type) {
	case int:
		v.Object = v.Object.(int) + int(n)
	case int8:
		v.Object = v.Object.(int8) + int8(n)
	case int16:
		v.Object = v.Object.(int16) + int16(n)
	case int32:
		v.Object = v.Object.(int32) + int32(n)
	case int64:
		v.Object = v.Object.(int64) + n
	case uint:
		v.Object = v.Object.(uint) + uint(n)
	case uintptr:
		v.Object = v.Object.(uintptr) + uintptr(n)
	case uint8:
		v.Object = v.Object.(uint8) + uint8(n)
	case uint16:
		v.Object = v.Object.(uint16) + uint16(n)
	case uint32:
		v.Object = v.Object.(uint32) + uint32(n)
	case uint64:
		v.Object = v.Object.(uint64) + uint64(n)
	case float32:
		v.Object = v.Object.(float32) + float32(n)
	case float64:
		v.Object = v.Object.(float64) + float64(n)
	default:

		return fmt.Errorf("The value for %s is not an integer", k)
	}
	c.safeStore(k, v)

	return nil
}

// Increment an item of type float32 or float64 by n. Returns an error if the
// item's value is not floating point, if it was not found, or if it is not
// possible to increment it by n. Pass a negative number to decrement the
// value. To retrieve the incremented value, use one of the specialized methods,
// e.g. IncrementFloat64.
func (c *cache) IncrementFloat(k string, n float64) error {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	switch v.Object.(type) {
	case float32:
		v.Object = v.Object.(float32) + float32(n)
	case float64:
		v.Object = v.Object.(float64) + n
	default:

		return fmt.Errorf("The value for %s does not have type float32 or float64", k)
	}
	c.safeStore(k, v)

	return nil
}

// Increment an item of type int by n. Returns an error if the item's value is
// not an int, or if it was not found. If there is no error, the incremented
// value is returned.
func (c *cache) IncrementInt(k string, n int) (int, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type int8 by n. Returns an error if the item's value is
// not an int8, or if it was not found. If there is no error, the incremented
// value is returned.
func (c *cache) IncrementInt8(k string, n int8) (int8, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int8)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int8", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type int16 by n. Returns an error if the item's value is
// not an int16, or if it was not found. If there is no error, the incremented
// value is returned.
func (c *cache) IncrementInt16(k string, n int16) (int16, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int16)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int16", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type int32 by n. Returns an error if the item's value is
// not an int32, or if it was not found. If there is no error, the incremented
// value is returned.
func (c *cache) IncrementInt32(k string, n int32) (int32, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int32)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int32", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type int64 by n. Returns an error if the item's value is
// not an int64, or if it was not found. If there is no error, the incremented
// value is returned.
func (c *cache) IncrementInt64(k string, n int64) (int64, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int64)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int64", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type uint by n. Returns an error if the item's value is
// not an uint, or if it was not found. If there is no error, the incremented
// value is returned.
func (c *cache) IncrementUint(k string, n uint) (uint, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type uintptr by n. Returns an error if the item's value
// is not an uintptr, or if it was not found. If there is no error, the
// incremented value is returned.
func (c *cache) IncrementUintptr(k string, n uintptr) (uintptr, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uintptr)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uintptr", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type uint8 by n. Returns an error if the item's value
// is not an uint8, or if it was not found. If there is no error, the
// incremented value is returned.
func (c *cache) IncrementUint8(k string, n uint8) (uint8, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint8)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint8", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type uint16 by n. Returns an error if the item's value
// is not an uint16, or if it was not found. If there is no error, the
// incremented value is returned.
func (c *cache) IncrementUint16(k string, n uint16) (uint16, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint16)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint16", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type uint32 by n. Returns an error if the item's value
// is not an uint32, or if it was not found. If there is no error, the
// incremented value is returned.
func (c *cache) IncrementUint32(k string, n uint32) (uint32, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint32)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint32", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type uint64 by n. Returns an error if the item's value
// is not an uint64, or if it was not found. If there is no error, the
// incremented value is returned.
func (c *cache) IncrementUint64(k string, n uint64) (uint64, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint64)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint64", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type float32 by n. Returns an error if the item's value
// is not an float32, or if it was not found. If there is no error, the
// incremented value is returned.
func (c *cache) IncrementFloat32(k string, n float32) (float32, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(float32)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an float32", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Increment an item of type float64 by n. Returns an error if the item's value
// is not an float64, or if it was not found. If there is no error, the
// incremented value is returned.
func (c *cache) IncrementFloat64(k string, n float64) (float64, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(float64)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an float64", k)
	}
	nv := rv + n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type int, int8, int16, int32, int64, uintptr, uint,
// uint8, uint32, or uint64, float32 or float64 by n. Returns an error if the
// item's value is not an integer, if it was not found, or if it is not
// possible to decrement it by n. To retrieve the decremented value, use one
// of the specialized methods, e.g. DecrementInt64.
func (c *cache) Decrement(k string, n int64) error {
	// TODO: Implement Increment and Decrement more cleanly.
	// (Cannot do Increment(k, n*-1) for uints.)

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return fmt.Errorf("Item not found")
	}
	v := item.(Item)
	switch v.Object.(type) {
	case int:
		v.Object = v.Object.(int) - int(n)
	case int8:
		v.Object = v.Object.(int8) - int8(n)
	case int16:
		v.Object = v.Object.(int16) - int16(n)
	case int32:
		v.Object = v.Object.(int32) - int32(n)
	case int64:
		v.Object = v.Object.(int64) - n
	case uint:
		v.Object = v.Object.(uint) - uint(n)
	case uintptr:
		v.Object = v.Object.(uintptr) - uintptr(n)
	case uint8:
		v.Object = v.Object.(uint8) - uint8(n)
	case uint16:
		v.Object = v.Object.(uint16) - uint16(n)
	case uint32:
		v.Object = v.Object.(uint32) - uint32(n)
	case uint64:
		v.Object = v.Object.(uint64) - uint64(n)
	case float32:
		v.Object = v.Object.(float32) - float32(n)
	case float64:
		v.Object = v.Object.(float64) - float64(n)
	default:

		return fmt.Errorf("The value for %s is not an integer", k)
	}
	c.safeStore(k, v)

	return nil
}

// Decrement an item of type float32 or float64 by n. Returns an error if the
// item's value is not floating point, if it was not found, or if it is not
// possible to decrement it by n. Pass a negative number to decrement the
// value. To retrieve the decremented value, use one of the specialized methods,
// e.g. DecrementFloat64.
func (c *cache) DecrementFloat(k string, n float64) error {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	switch v.Object.(type) {
	case float32:
		v.Object = v.Object.(float32) - float32(n)
	case float64:
		v.Object = v.Object.(float64) - n
	default:

		return fmt.Errorf("The value for %s does not have type float32 or float64", k)
	}
	c.safeStore(k, v)

	return nil
}

// Decrement an item of type int by n. Returns an error if the item's value is
// not an int, or if it was not found. If there is no error, the decremented
// value is returned.
func (c *cache) DecrementInt(k string, n int) (int, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type int8 by n. Returns an error if the item's value is
// not an int8, or if it was not found. If there is no error, the decremented
// value is returned.
func (c *cache) DecrementInt8(k string, n int8) (int8, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int8)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int8", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type int16 by n. Returns an error if the item's value is
// not an int16, or if it was not found. If there is no error, the decremented
// value is returned.
func (c *cache) DecrementInt16(k string, n int16) (int16, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int16)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int16", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type int32 by n. Returns an error if the item's value is
// not an int32, or if it was not found. If there is no error, the decremented
// value is returned.
func (c *cache) DecrementInt32(k string, n int32) (int32, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int32)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int32", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type int64 by n. Returns an error if the item's value is
// not an int64, or if it was not found. If there is no error, the decremented
// value is returned.
func (c *cache) DecrementInt64(k string, n int64) (int64, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(int64)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an int64", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type uint by n. Returns an error if the item's value is
// not an uint, or if it was not found. If there is no error, the decremented
// value is returned.
func (c *cache) DecrementUint(k string, n uint) (uint, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type uintptr by n. Returns an error if the item's value
// is not an uintptr, or if it was not found. If there is no error, the
// decremented value is returned.
func (c *cache) DecrementUintptr(k string, n uintptr) (uintptr, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uintptr)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uintptr", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type uint8 by n. Returns an error if the item's value is
// not an uint8, or if it was not found. If there is no error, the decremented
// value is returned.
func (c *cache) DecrementUint8(k string, n uint8) (uint8, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint8)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint8", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type uint16 by n. Returns an error if the item's value
// is not an uint16, or if it was not found. If there is no error, the
// decremented value is returned.
func (c *cache) DecrementUint16(k string, n uint16) (uint16, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint16)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint16", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type uint32 by n. Returns an error if the item's value
// is not an uint32, or if it was not found. If there is no error, the
// decremented value is returned.
func (c *cache) DecrementUint32(k string, n uint32) (uint32, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint32)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint32", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type uint64 by n. Returns an error if the item's value
// is not an uint64, or if it was not found. If there is no error, the
// decremented value is returned.
func (c *cache) DecrementUint64(k string, n uint64) (uint64, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(uint64)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an uint64", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type float32 by n. Returns an error if the item's value
// is not an float32, or if it was not found. If there is no error, the
// decremented value is returned.
func (c *cache) DecrementFloat32(k string, n float32) (float32, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(float32)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an float32", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Decrement an item of type float64 by n. Returns an error if the item's value
// is not an float64, or if it was not found. If there is no error, the
// decremented value is returned.
func (c *cache) DecrementFloat64(k string, n float64) (float64, error) {

	item, found := c.items.Load(k)
	if !found || item.(Item).Expired() {

		return 0, fmt.Errorf("Item %s not found", k)
	}
	v := item.(Item)
	rv, ok := v.Object.(float64)
	if !ok {

		return 0, fmt.Errorf("The value for %s is not an float64", k)
	}
	nv := rv - n
	v.Object = nv
	c.safeStore(k, v)

	return nv, nil
}

// Delete an item from the cache. Does nothing if the key is not in the cache.
func (c *cache) Delete(k string) {

	v, evicted := c.delete(k)

	if evicted {
		c.onEvicted(k, v)
	}
}

func (c *cache) delete(k string) (interface{}, bool) {
	if c.onEvicted != nil {
		if v, found := c.items.Load(k); found {
			c.safeDelete(k)
			return v.(Item).Object, true
		}
	}
	c.safeDelete(k)
	return nil, false
}

type keyAndValue struct {
	key   string
	value interface{}
}

// Delete all expired items from the cache.
func (c *cache) DeleteExpired() {
	var evictedItems []keyAndValue
	now := time.Now().UnixNano()

	c.items.Range(func(k, v interface{}) bool {
		if v.(Item).Expiration > 0 && now > v.(Item).Expiration {
			ov, evicted := c.delete(k.(string))
			if evicted {
				evictedItems = append(evictedItems, keyAndValue{k.(string), ov})
			}
		}
		return true
	})

	for _, v := range evictedItems {
		c.onEvicted(v.key, v.value)
	}
}

// Sets an (optional) function that is called with the key and value when an
// item is evicted from the cache. (Including when it is deleted manually, but
// not when it is overwritten.) Set to nil to disable.
func (c *cache) OnEvicted(f func(string, interface{})) {

	c.onEvicted = f

}

// Copies all unexpired items in the cache into a new map and returns it.
func (c *cache) Items() map[string]Item {

	var m map[string]Item

	now := time.Now().UnixNano()
	c.items.Range(func(k, v interface{}) bool {
		if v.(Item).Expiration > 0 {
			if now > v.(Item).Expiration {
				return true
			}
		}
		m[k.(string)] = v.(Item)
		return true
	})

	return m
}

// Returns the number of items in the cache. This may include items that have
// expired, but have not yet been cleaned up.
func (c *cache) ItemCount() uint32 {
	return c.counter.Load()
}

// Delete all items from the cache.
func (c *cache) Flush() {

	delete := func(key interface{}, value interface{}) bool {
		c.safeDelete(key)
		return true
	}
	c.items.Range(delete)

}

type janitor struct {
	Interval time.Duration
	stop     chan bool
}

func (j *janitor) Run(c *cache) {
	ticker := time.NewTicker(j.Interval)
	for {
		select {
		case <-ticker.C:
			c.DeleteExpired()
		case <-j.stop:
			ticker.Stop()
			return
		}
	}
}

func stopJanitor(c *Cache) {
	c.janitor.stop <- true
}

func runJanitor(c *cache, ci time.Duration) {
	j := &janitor{
		Interval: ci,
		stop:     make(chan bool),
	}
	c.janitor = j
	go j.Run(c)
}

func newCache(de time.Duration, m sync.Map) *cache {
	if de == 0 {
		de = -1
	}
	c := &cache{
		defaultExpiration: de,
		items:             m,
	}
	c.counter.Store(0)
	return c
}

func newCacheWithJanitor(de time.Duration, ci time.Duration, m sync.Map) *Cache {
	c := newCache(de, m)
	// This trick ensures that the janitor goroutine (which--granted it
	// was enabled--is running DeleteExpired on c forever) does not keep
	// the returned C object from being garbage collected. When it is
	// garbage collected, the finalizer stops the janitor goroutine, after
	// which c can be collected.
	C := &Cache{c}
	if ci > 0 {
		runJanitor(c, ci)
		runtime.SetFinalizer(C, stopJanitor)
	}
	return C
}

// Return a new cache with a given default expiration duration and cleanup
// interval. If the expiration duration is less than one (or NoExpiration),
// the items in the cache never expire (by default), and must be deleted
// manually. If the cleanup interval is less than one, expired items are not
// deleted from the cache before calling c.DeleteExpired().
func New(defaultExpiration, cleanupInterval time.Duration) *Cache {
	var items sync.Map
	return newCacheWithJanitor(defaultExpiration, cleanupInterval, items)
}

// Return a new cache with a given default expiration duration and cleanup
// interval. If the expiration duration is less than one (or NoExpiration),
// the items in the cache never expire (by default), and must be deleted
// manually. If the cleanup interval is less than one, expired items are not
// deleted from the cache before calling c.DeleteExpired().
//
// NewFrom() also accepts an items map which will serve as the underlying map
// for the cache. This is useful for starting from a deserialized cache
// (serialized using e.g. gob.Encode() on c.Items()), or passing in e.g.
// make(map[string]Item, 500) to improve startup performance when the cache
// is expected to reach a certain minimum size.
//
// Only the cache's methods synchronize access to this map, so it is not
// recommended to keep any references to the map around after creating a cache.
// If need be, the map can be accessed at a later point using c.Items() (subject
// to the same caveat.)
//
// Note regarding serialization: When using e.g. gob, make sure to
// gob.Register() the individual types stored in the cache before encoding a
// map retrieved with c.Items(), and to register those same types before
// decoding a blob containing an items map.
func NewFrom(defaultExpiration, cleanupInterval time.Duration, items sync.Map) *Cache {
	return newCacheWithJanitor(defaultExpiration, cleanupInterval, items)
}
