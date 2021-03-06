package filestorage

import (
	"encoding/json"
	"regexp"
	"strconv"

	"github.com/lovego/errs"
)

var objectRegexp = regexp.MustCompile(`^([\w.]+)\|(\d+)(\|(\w+))?$`)
var errInvalidObject = errs.New("args-err", "invalid LinkObject")

// LinkObject is a structured reference implementaion for link object string.
type LinkObject struct {
	Table string // Table name, required.
	ID    int64  // Primary key or unique key, required.
	Field string // If the table has multiple file storage fields, Field is required, Otherwise Field can be omitted.
}

func (o LinkObject) String() string {
	if o.Table == "" && o.ID == 0 && o.Field == "" {
		return ""
	}
	s := o.Table + "|" + strconv.FormatInt(o.ID, 10)
	if o.Field != "" {
		s += "|" + o.Field
	}
	return s
}

// MarshalJSON implements json.Marshaler
func (o LinkObject) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.String())
}

// UnmarshalJSON implements json.Unmarshaler
func (o *LinkObject) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		return errInvalidObject
	}
	var s string
	if b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	} else {
		s = string(b)
	}
	if s == "" {
		return nil
	}

	m := objectRegexp.FindStringSubmatch(s)
	if len(m) == 0 {
		return errInvalidObject
	}
	o.Table = m[1]
	if id, err := strconv.ParseInt(m[2], 10, 64); err != nil {
		return errInvalidObject
	} else {
		o.ID = id
	}
	o.Field = m[4]
	return nil
}
