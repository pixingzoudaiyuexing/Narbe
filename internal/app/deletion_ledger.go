package app

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pixingzoudaiyuexing/Narbe/internal/cloud"
)

// A creation record is an ownership hint, not proof that resources still exist
// or that deleting the VM ends their charges. Only the provider can validate scope.
func (a *App) deletionLedger(ctx context.Context, b mutation, p cloud.Provider) *cloud.ResourceLedger {
	unknown := &cloud.ResourceLedger{Status: "unknown"}
	matcher, ok := p.(interface {
		MatchesCreation(context.Context, cloud.Spec, string, cloud.Operation, string) bool
	})
	if !ok {
		return unknown
	}
	rows, e := a.S.DB.Query("SELECT id,payload,result,state FROM jobs WHERE account_id=? AND action='create' AND state IN ('succeeded','unknown','resolved') AND lower(CASE WHEN json_valid(result) THEN json_extract(result,'$.resourceId') END)=lower(?)", b.AccountID, b.Target)
	if e != nil {
		return unknown
	}
	// Release the single SQLite connection before provider calls or other queries.
	type candidate struct {
		id, state       string
		payload, result []byte
	}
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if e = rows.Scan(&c.id, &c.payload, &c.result, &c.state); e != nil {
			rows.Close()
			return unknown
		}
		candidates = append(candidates, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(candidates) != 1 {
		return unknown
	}
	c := candidates[0]
	raw, e := a.S.Unseal(c.payload, "job:"+c.id)
	if e != nil {
		return unknown
	}
	var source mutation
	var prior cloud.Operation
	if json.Unmarshal(raw, &source) != nil || json.Unmarshal(c.result, &prior) != nil {
		return unknown
	}
	if source.ID != c.id || source.Action != "create" || source.AccountID != b.AccountID || source.AccountRevision != b.AccountRevision || source.Target != "" || source.Name == "" || source.Spec.Name != source.Name || source.Region != source.Spec.Region || !strings.EqualFold(source.Region, b.Region) || !strings.EqualFold(prior.Region, b.Region) {
		return unknown
	}
	if !matcher.MatchesCreation(ctx, source.Spec, c.id, prior, b.Target) {
		return unknown
	}
	kinds := []string{"public-ip", "nic", "nsg", "disk"}
	if len(prior.Owned) != len(kinds) {
		return unknown
	}
	ledger := &cloud.ResourceLedger{Status: "linked", SourceJobID: c.id, SourceState: c.state}
	for n, id := range prior.Owned {
		ledger.Resources = append(ledger.Resources, cloud.RelatedResource{ID: id, Kind: kinds[n], State: "unverified"})
	}
	return ledger
}
