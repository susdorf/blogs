package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"time"
)

// ============================================================================
// Domain Types
// ============================================================================

type Aggregate interface {
	GetDomain() string
	GetID() string
}

type DomainEvent interface {
	GetEventType() string
}

type Order struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	Total  float64 `json:"total"`
	Items  []Item  `json:"items"`
}

func (o *Order) GetDomain() string { return "orders" }
func (o *Order) GetID() string     { return o.ID }

type Item struct {
	SKU      string  `json:"sku"`
	Quantity int     `json:"quantity"`
	Price    float64 `json:"price"`
}

type OrderPlacedEvent struct {
	OrderID string `json:"orderId"`
	Total   float64 `json:"total"`
}

func (e *OrderPlacedEvent) GetEventType() string { return "OrderPlaced" }

type PlaceOrderCommand struct {
	OrderID string
	Items   []Item
}

// Compile-time interface verification
var _ Aggregate = (*Order)(nil)
var _ DomainEvent = (*OrderPlacedEvent)(nil)

// ============================================================================
// Reflection Utilities
// ============================================================================

func IsNil(val any) bool {
	if val == nil {
		return true
	}
	v := reflect.ValueOf(val)
	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Array, reflect.Chan, reflect.Slice, reflect.Interface:
		return v.IsNil()
	}
	return false
}

func GetTypeName(val any) string {
	if IsNil(val) {
		return ""
	}
	t := reflect.TypeOf(val)
	if t.Kind() == reflect.Ptr {
		return t.Elem().Name()
	}
	return t.Name()
}

func GetTypePath(val any) string {
	if IsNil(val) {
		return ""
	}
	t := reflect.TypeOf(val)
	switch t.Kind() {
	case reflect.Ptr:
		return t.Elem().PkgPath() + "." + t.Elem().Name()
	case reflect.Func:
		ptr := reflect.ValueOf(val).Pointer()
		fn := runtime.FuncForPC(ptr)
		if fn == nil {
			return ""
		}
		return fn.Name()
	default:
		return t.PkgPath() + "." + t.Name()
	}
}

func NewInstanceOf[T any]() T {
	var instance T
	instanceType := reflect.TypeOf(instance)
	if instanceType == nil {
		return instance
	}
	if instanceType.Kind() == reflect.Ptr {
		newInstance := reflect.New(instanceType.Elem())
		return newInstance.Interface().(T)
	}
	return instance
}

func InterfaceToInt64(val any) int64 {
	switch v := val.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			return parsed
		}
		return 0
	default:
		return 0
	}
}

func InterfaceToMap(val any) (map[string]any, error) {
	if val == nil {
		return nil, nil
	}
	data, err := json.Marshal(val)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = json.Unmarshal(data, &result)
	return result, err
}

func FindInterfaceByKey(val any, key string) (any, bool) {
	mobj, ok := val.(map[string]any)
	if !ok {
		return nil, false
	}
	for k, v := range mobj {
		if k == key {
			return v, true
		}
		if m, ok := v.(map[string]any); ok {
			if res, ok := FindInterfaceByKey(m, key); ok {
				return res, true
			}
		}
		if arr, ok := v.([]any); ok {
			for _, elem := range arr {
				if res, ok := FindInterfaceByKey(elem, key); ok {
					return res, true
				}
			}
		}
	}
	return nil, false
}

func DeepCopy[T any](src T) (T, bool) {
	var zero T
	if IsNil(src) {
		return zero, false
	}
	data, err := json.Marshal(src)
	if err != nil {
		return zero, false
	}
	var dst T
	if err := json.Unmarshal(data, &dst); err != nil {
		return zero, false
	}
	return dst, true
}

// ============================================================================
// Main Demo
// ============================================================================

func main() {
	fmt.Println("=== Reflection Done Right Demo ===")
	fmt.Println()

	// 1. Nil Checking
	fmt.Println("1. Nil Checking (the tricky interface-nil case):")
	var order *Order = nil
	var agg Aggregate = order
	fmt.Printf("   order == nil:      %v\n", order == nil)
	fmt.Printf("   agg == nil:        %v (interface contains nil pointer!)\n", agg == nil)
	fmt.Printf("   IsNil(order):      %v\n", IsNil(order))
	fmt.Printf("   IsNil(agg):        %v (correctly detects nil inside interface)\n", IsNil(agg))
	fmt.Println()

	// 2. Getting Type Names
	fmt.Println("2. Getting Type Names:")
	cmd := &PlaceOrderCommand{OrderID: "123"}
	evt := &OrderPlacedEvent{OrderID: "123", Total: 99.99}
	validOrder := &Order{ID: "order-1", Status: "placed"}
	fmt.Printf("   GetTypeName(cmd):   %q\n", GetTypeName(cmd))
	fmt.Printf("   GetTypeName(evt):   %q\n", GetTypeName(evt))
	fmt.Printf("   GetTypeName(order): %q\n", GetTypeName(validOrder))
	fmt.Printf("   GetTypeName(nil):   %q\n", GetTypeName(nil))
	fmt.Println()

	// 3. Getting Fully Qualified Type Paths
	fmt.Println("3. Fully Qualified Type Paths:")
	fmt.Printf("   GetTypePath(cmd): %q\n", GetTypePath(cmd))
	fmt.Printf("   GetTypePath(evt): %q\n", GetTypePath(evt))
	fmt.Printf("   GetTypePath(func): %q\n", GetTypePath(main))
	fmt.Println()

	// 4. Dynamic Instance Creation
	fmt.Println("4. Dynamic Instance Creation with Generics:")
	newOrder := NewInstanceOf[*Order]()
	fmt.Printf("   NewInstanceOf[*Order](): %+v (valid pointer to zero Order)\n", newOrder)
	newItem := NewInstanceOf[Item]()
	fmt.Printf("   NewInstanceOf[Item]():   %+v (zero value struct)\n", newItem)
	fmt.Println()

	// 5. Type Conversion
	fmt.Println("5. Safe Type Conversion:")
	fmt.Printf("   InterfaceToInt64(42):        %d\n", InterfaceToInt64(42))
	fmt.Printf("   InterfaceToInt64(int64(99)): %d\n", InterfaceToInt64(int64(99)))
	fmt.Printf("   InterfaceToInt64(\"123\"):     %d\n", InterfaceToInt64("123"))
	fmt.Printf("   InterfaceToInt64(\"abc\"):     %d (invalid string returns 0)\n", InterfaceToInt64("abc"))
	fmt.Printf("   InterfaceToInt64(3.14):      %d (unsupported type returns 0)\n", InterfaceToInt64(3.14))
	fmt.Println()

	// 6. Interface to Map
	fmt.Println("6. Interface to Map Conversion:")
	orderWithItems := &Order{
		ID:     "order-123",
		Status: "placed",
		Total:  149.99,
		Items:  []Item{{SKU: "ABC", Quantity: 2, Price: 49.99}},
	}
	m, _ := InterfaceToMap(orderWithItems)
	fmt.Printf("   Order as map:\n")
	for k, v := range m {
		fmt.Printf("      %s: %v\n", k, v)
	}
	fmt.Println()

	// 7. Finding Values in Nested Structures
	fmt.Println("7. Finding Values in Nested Structures:")
	nestedData := map[string]any{
		"user": map[string]any{
			"profile": map[string]any{
				"email":    "user@example.com",
				"verified": true,
			},
			"settings": map[string]any{
				"theme": "dark",
			},
		},
		"orders": []any{
			map[string]any{"id": "order-1", "total": 99.99},
			map[string]any{"id": "order-2", "total": 149.99},
		},
	}
	email, found := FindInterfaceByKey(nestedData, "email")
	fmt.Printf("   FindInterfaceByKey(data, \"email\"):    %v (found: %v)\n", email, found)
	theme, found := FindInterfaceByKey(nestedData, "theme")
	fmt.Printf("   FindInterfaceByKey(data, \"theme\"):    %v (found: %v)\n", theme, found)
	total, found := FindInterfaceByKey(nestedData, "total")
	fmt.Printf("   FindInterfaceByKey(data, \"total\"):    %v (found: %v) (first match)\n", total, found)
	missing, found := FindInterfaceByKey(nestedData, "missing")
	fmt.Printf("   FindInterfaceByKey(data, \"missing\"): %v (found: %v)\n", missing, found)
	fmt.Println()

	// 8. Deep Copy
	fmt.Println("8. Deep Copy (true copy, not pointer copy):")
	original := &Order{
		ID:     "order-1",
		Status: "placed",
		Items:  []Item{{SKU: "ABC", Quantity: 1, Price: 29.99}},
	}
	copied, ok := DeepCopy(original)
	fmt.Printf("   Original: %+v\n", original)
	fmt.Printf("   DeepCopy successful: %v\n", ok)

	// Modify copy
	copied.Status = "shipped"
	copied.Items[0].SKU = "XYZ"
	fmt.Printf("   After modifying copy:\n")
	fmt.Printf("      Original.Status: %q (unchanged)\n", original.Status)
	fmt.Printf("      Copied.Status:   %q\n", copied.Status)
	fmt.Printf("      Original.Items[0].SKU: %q (unchanged)\n", original.Items[0].SKU)
	fmt.Printf("      Copied.Items[0].SKU:   %q\n", copied.Items[0].SKU)
	fmt.Println()

	// 9. Compile-time Interface Verification
	fmt.Println("9. Compile-time Interface Verification:")
	fmt.Println("   The following lines verify interfaces at compile time:")
	fmt.Println("      var _ Aggregate = (*Order)(nil)")
	fmt.Println("      var _ DomainEvent = (*OrderPlacedEvent)(nil)")
	fmt.Println("   -> If Order didn't implement Aggregate, this file wouldn't compile!")
	fmt.Println()

	// 10. Performance Comparison
	fmt.Println("10. Performance Comparison:")
	iterations := 100000

	// Direct field access
	start := time.Now()
	for i := 0; i < iterations; i++ {
		_ = validOrder.Status
	}
	directTime := time.Since(start)

	// Reflection access
	start = time.Now()
	for i := 0; i < iterations; i++ {
		v := reflect.ValueOf(validOrder).Elem()
		_ = v.FieldByName("Status").String()
	}
	reflectTime := time.Since(start)

	// Type name with caching simulation
	var typeCache = make(map[reflect.Type]string)
	start = time.Now()
	for i := 0; i < iterations; i++ {
		t := reflect.TypeOf(validOrder)
		if _, ok := typeCache[t]; !ok {
			typeCache[t] = t.Elem().Name()
		}
	}
	cachedTime := time.Since(start)

	fmt.Printf("   %d iterations:\n", iterations)
	fmt.Printf("      Direct field access:    %v\n", directTime)
	fmt.Printf("      Reflection access:      %v (~%.0fx slower)\n", reflectTime, float64(reflectTime)/float64(directTime))
	fmt.Printf("      Cached type lookup:     %v\n", cachedTime)
	fmt.Println()

	fmt.Println("=== Demo Complete ===")
}
