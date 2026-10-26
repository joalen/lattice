package ports

import "fmt"

// OpKind identifies a write operation inside SetStore.Apply
type OpKind int

const (
	OpAdd    OpKind = iota + 1 // add Members to the set at Key
	OpRemove                   // remove Members from the set at Key
	OpDelete                   // delete the whole set at Key
	OpPutDoc                   // store Raw as document Entity/ID
	OpDelDoc                   // delete document Entity/ID
)

// Op is one write.
type Op struct {
	Kind    OpKind
	Key     string
	Members []string
	Entity  string
	ID      string
	Raw     []byte
}

func Add(key string, members ...string) Op    { return Op{Kind: OpAdd, Key: key, Members: members} }
func Remove(key string, members ...string) Op { return Op{Kind: OpRemove, Key: key, Members: members} }
func DeleteKey(key string) Op                 { return Op{Kind: OpDelete, Key: key} }
func PutDoc(entity, id string, raw []byte) Op {
	return Op{Kind: OpPutDoc, Entity: entity, ID: id, Raw: raw}
}
func DelDoc(entity, id string) Op { return Op{Kind: OpDelDoc, Entity: entity, ID: id} }

// Validate is shared by every adapter
func (o Op) Validate() error {
	switch o.Kind {
	case OpAdd, OpRemove, OpDelete:
		if o.Key == "" {
			return fmt.Errorf("ports: op kind %d needs a key", o.Kind)
		}
	case OpPutDoc, OpDelDoc:
		if o.Entity == "" || o.ID == "" {
			return fmt.Errorf("ports: op kind %d needs an entity and id", o.Kind)
		}
	default:
		return fmt.Errorf("ports: unknown op kind %d", o.Kind)
	}
	return nil
}
