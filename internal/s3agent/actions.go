// Lifecycle handlers reconcile only resources with verified service ownership.
// Deletion is durable and fail-closed; no secret or local recovery database is persisted.
package s3agent

import (
	"context"
	"errors"
	"strings"
	"time"
)

func validateProperties(p *Properties) error {
	if p == nil || p.QuotaBytes <= 0 || p.QuotaBytes > MaxQuota {
		return errors.New("quotaBytes must be a positive JSON-safe integer")
	}
	if p.SecretAccessKey != nil {
		secret := *p.SecretAccessKey
		if len(secret) < 8 || strings.HasPrefix(secret, "vault://") {
			return ErrSecretRequired
		}
	}
	return nil
}

func (a *Agent) ownedBucket(ctx context.Context, n Names, serviceID string) (map[string]string, error) {
	tags, err := a.backend.BucketTags(ctx, n.Bucket)
	if err != nil {
		return nil, err
	}
	if tags[tagService] != serviceID || tags[tagAgent] != a.id {
		return nil, ErrOwnership
	}
	switch tags[tagPhase] {
	case provisioning, active, deleting:
	default:
		return nil, ErrOwnership
	}
	return tags, nil
}

// A policy marker AND the complete expected permissions prove policy ownership.
// User ownership additionally requires its exclusive policy binding and no groups.
func (a *Agent) inspectIAM(ctx context.Context, n Names) (policyExists, userExists bool, user User, err error) {
	policy, err := a.backend.GetPolicy(ctx, n.Policy)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, false, User{}, err
	}
	policyExists = err == nil
	if policyExists && canonicalPolicy(policy) != canonicalPolicy(policyFor(n)) {
		return false, false, User{}, ErrOwnership
	}
	user, err = a.backend.GetUser(ctx, n.Key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, false, User{}, err
	}
	userExists = err == nil
	if userExists && (!policyExists || user.PolicyName != n.Policy || len(user.MemberOf) != 0 ||
		(user.Status != "enabled" && user.Status != "disabled")) {
		return policyExists, userExists, user, ErrOwnership
	}
	return policyExists, userExists, user, nil
}

func (a *Agent) phase(ctx context.Context, n Names, tags map[string]string, phase string) error {
	copy := make(map[string]string, len(tags))
	for k, v := range tags {
		copy[k] = v
	}
	copy[tagPhase] = phase
	if err := a.backend.PutBucketTags(ctx, n.Bucket, copy); err != nil {
		return err
	}
	actual, err := a.backend.BucketTags(ctx, n.Bucket)
	if err != nil {
		return err
	}
	for _, key := range []string{tagService, tagAgent, tagPhase} {
		if actual[key] != copy[key] {
			return ErrOwnership
		}
	}
	return nil
}

func (a *Agent) waitUsage(ctx context.Context, n Names, quota uint64) error {
	for {
		usage, err := a.backend.Usage(ctx, n.Bucket)
		if err == nil {
			if usage.Quota != quota {
				return errors.New("quota readback mismatch")
			}
			return nil
		}
		if !errors.Is(err, ErrNotReady) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (a *Agent) create(ctx context.Context, n Names, serviceID string, p *Properties) (uint64, error) {
	if err := validateProperties(p); err != nil {
		return 0, err
	}
	target := uint64(p.QuotaBytes)
	tags, err := a.ownedBucket(ctx, n, serviceID)
	bucketExists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	if bucketExists && tags[tagPhase] == deleting {
		return 0, ErrDeleting
	}
	policyExists, userExists, _, err := a.inspectIAM(ctx, n)
	if err != nil {
		return 0, err
	}
	if !bucketExists && (policyExists || userExists) {
		return 0, ErrOwnership
	}
	current := uint64(0)
	if bucketExists {
		current, err = a.backend.GetQuota(ctx, n.Bucket)
		if err != nil {
			return 0, err
		}
		if target < current {
			return 0, ErrQuotaDecrease
		}
	}
	// A lost completion acknowledgement can be recovered without recovering the secret.
	if p.SecretAccessKey == nil {
		if !bucketExists || tags[tagPhase] != active || !userExists || current != target {
			return 0, ErrSecretRequired
		}
		if err = a.waitUsage(ctx, n, target); err != nil {
			return 0, err
		}
		return a.setAccess(ctx, n, serviceID, true)
	}
	if !bucketExists {
		if err = a.backend.CreateBucket(ctx, n.Bucket); err != nil {
			return 0, err
		}
		tags = map[string]string{tagService: serviceID, tagAgent: a.id, tagPhase: provisioning}
	}
	// A crash before tagging leaves an unowned bucket: never adopt it on a later retry.
	if err = a.phase(ctx, n, tags, provisioning); err != nil {
		return 0, err
	}
	if !policyExists {
		if err = a.backend.PutPolicy(ctx, n.Policy, policyFor(n)); err != nil {
			return 0, err
		}
	}
	if !userExists {
		if err = a.backend.CreateUser(ctx, n.Key, *p.SecretAccessKey); err != nil {
			return 0, err
		}
		// RustFS add-user does not bind the policy. A crash before binding is an
		// ambiguous-ownership window and requires operator reconciliation.
		if err = a.backend.BindPolicy(ctx, n.Key, n.Policy); err != nil {
			return 0, err
		}
	} else {
		if err = a.backend.SetUserStatus(ctx, n.Key, false); err != nil {
			return 0, err
		}
		if err = a.backend.SetSecret(ctx, n.Key, *p.SecretAccessKey); err != nil {
			return 0, err
		}
	}
	if _, exists, _, e := a.inspectIAM(ctx, n); e != nil || !exists {
		if e != nil {
			return 0, e
		}
		return 0, ErrOwnership
	}
	if current != target {
		if err = a.backend.SetQuota(ctx, n.Bucket, target); err != nil {
			return 0, err
		}
	}
	actual, err := a.backend.GetQuota(ctx, n.Bucket)
	if err != nil {
		return 0, err
	}
	if actual != target {
		return 0, errors.New("quota readback mismatch")
	}
	if err = a.waitUsage(ctx, n, target); err != nil {
		return 0, err
	}
	if err = a.phase(ctx, n, tags, active); err != nil {
		return 0, err
	}
	if err = a.backend.SetUserStatus(ctx, n.Key, true); err != nil {
		return 0, err
	}
	_, exists, currentUser, err := a.inspectIAM(ctx, n)
	if err != nil {
		return 0, err
	}
	if !exists || currentUser.Status != "enabled" {
		return 0, errors.New("credential enablement readback mismatch")
	}
	return target, nil
}

func (a *Agent) ready(ctx context.Context, n Names, serviceID string) (uint64, error) {
	tags, err := a.ownedBucket(ctx, n, serviceID)
	if err != nil {
		return 0, err
	}
	if tags[tagPhase] == deleting {
		return 0, ErrDeleting
	}
	if tags[tagPhase] != active {
		return 0, ErrNotReady
	}
	_, userExists, _, err := a.inspectIAM(ctx, n)
	if err != nil {
		return 0, err
	}
	if !userExists {
		return 0, ErrOwnership
	}
	quota, err := a.backend.GetQuota(ctx, n.Bucket)
	if err != nil {
		return 0, err
	}
	if quota == 0 {
		return 0, errors.New("mandatory backend quota is absent")
	}
	return quota, nil
}

func (a *Agent) update(ctx context.Context, n Names, serviceID string, p *Properties) (uint64, error) {
	if err := validateProperties(p); err != nil {
		return 0, err
	}
	current, err := a.ready(ctx, n, serviceID)
	if err != nil {
		return 0, err
	}
	target := uint64(p.QuotaBytes)
	if target < current {
		return 0, ErrQuotaDecrease
	}
	if target != current {
		if err = a.backend.SetQuota(ctx, n.Bucket, target); err != nil {
			return 0, err
		}
		actual, e := a.backend.GetQuota(ctx, n.Bucket)
		if e != nil {
			return 0, e
		}
		if actual != target {
			return 0, errors.New("quota readback mismatch")
		}
	}
	if p.SecretAccessKey != nil {
		if err = a.backend.SetSecret(ctx, n.Key, *p.SecretAccessKey); err != nil {
			return 0, err
		}
	}
	return target, nil
}

func (a *Agent) setAccess(ctx context.Context, n Names, serviceID string, enabled bool) (uint64, error) {
	quota, err := a.ready(ctx, n, serviceID)
	if err != nil {
		return 0, err
	}
	if err = a.backend.SetUserStatus(ctx, n.Key, enabled); err != nil {
		return 0, err
	}
	user, err := a.backend.GetUser(ctx, n.Key)
	if err != nil {
		return 0, err
	}
	expected := "disabled"
	if enabled {
		expected = "enabled"
	}
	if user.Status != expected {
		return 0, errors.New("credential status readback mismatch")
	}
	return quota, nil
}

func (a *Agent) delete(ctx context.Context, n Names, serviceID string) error {
	tags, err := a.ownedBucket(ctx, n, serviceID)
	bucketExists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	policyExists, userExists, _, err := a.inspectIAM(ctx, n)
	if err != nil {
		return err
	}
	if bucketExists {
		// Persist intent BEFORE disabling credentials or deleting any data.
		// Even if core times out without a lifecycle transition, resume stays forbidden.
		if err = a.phase(ctx, n, tags, deleting); err != nil {
			return err
		}
	}
	if userExists {
		if err = a.backend.SetUserStatus(ctx, n.Key, false); err != nil {
			return err
		}
		user, e := a.backend.GetUser(ctx, n.Key)
		if e != nil {
			return e
		}
		if user.Status != "disabled" {
			return errors.New("credential disablement readback mismatch")
		}
	}
	if bucketExists {
		if err = a.backend.PurgeBucket(ctx, n.Bucket); err != nil {
			return opError("force delete incomplete", err)
		}
	}
	// Retain the tagged bucket until IAM cleanup succeeds, preserving ownership evidence.
	if userExists {
		if err = a.backend.DeleteUser(ctx, n.Key); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if policyExists {
		if err = a.backend.DeletePolicy(ctx, n.Policy); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	if bucketExists {
		if err = a.backend.DeleteBucket(ctx, n.Bucket); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	// A successful mutation response alone is not evidence of complete cleanup.
	if _, err = a.backend.BucketTags(ctx, n.Bucket); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return errors.New("bucket deletion not confirmed")
	}
	if _, err = a.backend.GetUser(ctx, n.Key); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return errors.New("credential deletion not confirmed")
	}
	if _, err = a.backend.GetPolicy(ctx, n.Policy); !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		return errors.New("policy deletion not confirmed")
	}
	return nil
}
