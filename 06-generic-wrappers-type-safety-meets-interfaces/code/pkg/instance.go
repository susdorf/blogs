package pkg

import (
	"encoding/json"
	"reflect"
)

// NewInstanceOf creates a new instance of a generic type T.
//
// This function checks whether the provided type is a pointer type. If it is,
// it creates a new instance of the type that the pointer points to. Otherwise,
// it returns a zero value of the type.
func NewInstanceOf[T any]() T {
	var instance T
	instanceType := reflect.TypeOf(instance)

	// Check if the type is a pointer type.
	if instanceType.Kind() == reflect.Ptr {
		// Create a new instance of the type the pointer points to.
		return reflect.New(instanceType.Elem()).Interface().(T)
	}

	// Return the zero value for the type.
	return instance
}

// Serialize converts a generic instance into a JSON-encoded byte slice.
func Serialize[T any](inst T) ([]byte, error) {
	return json.Marshal(inst)
}

// Deserialize decodes a JSON-encoded byte slice into a new instance of type T.
func Deserialize[T any](data []byte, inst T) (T, error) {
	copy := NewInstanceOf[T]()
	err := json.Unmarshal(data, &copy)
	if err != nil {
		var zero T
		return zero, err
	}
	return copy, nil
}
