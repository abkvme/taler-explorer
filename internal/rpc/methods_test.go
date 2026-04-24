package rpc

import (
	"encoding/json"
	"testing"
)

func TestDifficultyObject(t *testing.T) {
	raw := []byte(`{"chain":"main","blocks":4800568,"headers":4800568,"bestblockhash":"abc","difficulty":{"proof-of-work":0.0142,"proof-of-stake":0.00184,"search-interval":0}}`)
	var bi BlockchainInfo
	if err := json.Unmarshal(raw, &bi); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if bi.Difficulty.POW != 0.0142 || bi.Difficulty.POS != 0.00184 {
		t.Fatalf("got %+v", bi.Difficulty)
	}
}

func TestDifficultyScalar(t *testing.T) {
	raw := []byte(`{"chain":"main","blocks":1,"headers":1,"bestblockhash":"z","difficulty":123.45}`)
	var bi BlockchainInfo
	if err := json.Unmarshal(raw, &bi); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if bi.Difficulty.POW != 123.45 || bi.Difficulty.POS != 0 {
		t.Fatalf("got %+v", bi.Difficulty)
	}
}
