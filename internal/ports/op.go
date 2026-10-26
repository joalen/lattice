package ports

import (
	"fmt"
	"math"
)

// The zero OpKind is invalid on purpose, so an unset Kind fails Validate.
type OpKind int

const (
	OpAdd OpKind = iota + 1
	OpRemove
	OpDelete
	OpPutDoc
	OpDelDoc
	OpSetScore
	OpDelScore
)

type Op struct {
	Kind    OpKind
	Key     string
	Members []string
	Entity  string
	ID      string
	Raw     []byte
	Member  string
	Score   float64
}

func Add(key string, members ...string) Op    { return Op{Kind: OpAdd, Key: key, Members: members} }
func Remove(key string, members ...string) Op { return Op{Kind: OpRemove, Key: key, Members: members} }
func DeleteKey(key string) Op                 { return Op{Kind: OpDelete, Key: key} }
func PutDoc(entity, id string, raw []byte) Op {
	return Op{Kind: OpPutDoc, Entity: entity, ID: id, Raw: raw}
}
func DelDoc(entity, id string) Op { return Op{Kind: OpDelDoc, Entity: entity, ID: id} }
func SetScore(key, member string, score float64) Op {
	return Op{Kind: OpSetScore, Key: key, Member: member, Score: score}
}
func DelScore(key, member string) Op { return Op{Kind: OpDelScore, Key: key, Member: member} }

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
	case OpSetScore, OpDelScore:
		if o.Key == "" || o.Member == "" {
			return fmt.Errorf("ports: op kind %d needs a key and member", o.Kind)
		}
		if math.IsNaN(o.Score) {
			return fmt.Errorf("ports: score for %q is NaN", o.Member)
		}
	default:
		return fmt.Errorf("ports: unknown op kind %d", o.Kind)
	}
	return nil
}
