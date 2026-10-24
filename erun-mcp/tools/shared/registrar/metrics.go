package registrar

import (
	"reflect"
)

// mcpCallResultLabel uses err for "error", else a CommandOutput's Executed flag for
// preview; tools without that field are always "success".
func mcpCallResultLabel(out any, err error) string {
	if err != nil {
		return "error"
	}
	if executed, ok := commandOutputExecuted(out); ok && !executed {
		return "dry_run"
	}
	return "success"
}

func commandOutputExecuted(out any) (executed bool, ok bool) {
	value := reflect.ValueOf(out)
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return false, false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false, false
	}
	field := value.FieldByName("Executed")
	if !field.IsValid() || field.Kind() != reflect.Bool {
		return false, false
	}
	return field.Bool(), true
}
