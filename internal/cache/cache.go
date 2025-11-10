package cache

import (
	"sync"
	"time"
)

type Cache struct {
	sync.RWMutex
	items map[string]Item
}

type Item struct {
	Value      interface{}
	Expiration int64
}

func NewCache() *Cache {
	cache := &Cache{
		items: make(map[string]Item),
	}
	go cache.startCleanup()
	return cache
}

func (c *Cache) Set(key string, value interface{}, duration time.Duration) {
	c.Lock()
	defer c.Unlock()

	expiration := time.Now().Add(duration).UnixNano()
	c.items[key] = Item{
		Value:      value,
		Expiration: expiration,
	}
}

func (c *Cache) Get(key string) (interface{}, bool) {
	c.RLock()
	defer c.RUnlock()

	item, exists := c.items[key]
	if !exists {
		return nil, false
	}

	if item.Expiration > 0 && time.Now().UnixNano() > item.Expiration {
		return nil, false
	}

	return item.Value, true
}

func (c *Cache) Delete(key string) {
	c.Lock()
	defer c.Unlock()
	delete(c.items, key)
}

func (c *Cache) startCleanup() {
	ticker := time.NewTicker(time.Minute)
	for range ticker.C {
		c.cleanup()
	}
}

func (c *Cache) cleanup() {
	c.Lock()
	defer c.Unlock()

	now := time.Now().UnixNano()
	for key, item := range c.items {
		if item.Expiration > 0 && now > item.Expiration {
			delete(c.items, key)
		}
	}
}
