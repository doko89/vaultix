# RustFS presigned-URL incompatibility (SigV4 query auth)

Upstream: [Vaultix issue #1](https://github.com/doko89/vaultix/issues/1) —
RustFS 1.0.0 rejects ALL SigV4 query-auth presigned PUTs with
`403 SignatureDoesNotMatch`, even AWS-CLI-generated ones, while header
auth succeeds. This is a **server-side defect**, not a Vaultix bug.

## What breaks

Anything that uploads via a presigned URL (SigV4 **query** authentication)
against an affected server:

- Vaultix agent uploads in the default `presigned` upload mode (the manager
  mints short-lived presigned PUT grants; the agent PUTs bytes straight to
  storage — see `internal/adapter/s3/doc.go`).
- Interoperating tools doing the same thing: `aws s3 presign` + `curl`,
  SDK presigners (`aws-sdk-go-v2`, `boto3`, …), shared/download links —
  anything that signs into the query string instead of headers.

Header-auth SigV4 (`aws s3 cp`, SDK clients with static credentials) keeps
working, which is exactly what makes this defect confusing: credentials,
buckets, and networking are all fine.

## Affected versions

- Reported against **RustFS 1.0.0**. RustFS versions are not exhaustively
  mapped — **verify for your build** by running the control test below
  against your own endpoint; do not trust version numbers alone.

## Control test (proves it on any S3 endpoint)

Run from any host with AWS CLI v2 and `curl`. Uses placeholders only —
substitute your endpoint, bucket, and credentials. Use the same region
your destination is configured with (`us-east-1` below is the common
default for S3-compatible servers).

```bash
export AWS_ACCESS_KEY_ID=<your-access-key-id>
export AWS_SECRET_ACCESS_KEY=<your-secret-access-key>
export AWS_EC2_METADATA_DISABLED=true
ENDPOINT=http://<s3-host>:9000
echo hello > /tmp/vaultix-probe.txt

# 1. Positive control: header-auth upload. Expect success.
aws --endpoint-url "$ENDPOINT" --region us-east-1 s3 cp \
  /tmp/vaultix-probe.txt s3://<bucket>/vaultix-probe/header-auth.txt

# 2. Query-auth probe: presigned PUT. Capture the HTTP status + body.
URL=$(aws --endpoint-url "$ENDPOINT" --region us-east-1 s3 presign \
  s3://<bucket>/vaultix-probe/query-auth.txt --expires-in 300)
curl -sS -o /tmp/vaultix-presign-resp.txt -w 'HTTP %{http_code}\n' \
  -X PUT --upload-file /tmp/vaultix-probe.txt "$URL"
cat /tmp/vaultix-presign-resp.txt
```

Reading the outcome:

| Step 1 (`s3 cp`) | Step 2 (presigned PUT) | Meaning |
|---|---|---|
| succeeds | `HTTP 200` | Query auth works — server not affected. |
| succeeds | `HTTP 403` + `SignatureDoesNotMatch` | **Affected server.** Even AWS-CLI-minted URLs fail, so no client-side fix (Vaultix included) can help; remediate below. |
| fails | anything | Inconclusive — fix credentials, bucket, region, or network first, then re-run. |

(If your endpoint uses a self-signed cert, add `--no-verify-ssl` to the
`aws` commands and `-k` to the `curl` command. If step 1 fails with an
addressing error, retry with `AWS_S3_ADDRESSING_STYLE=path` exported.)

## Remediation decision tree

1. **Prefer a fixed server version.** Upgrade RustFS to a release whose
   query-auth verifies (re-run the control test after upgrading — step 2
   must return `HTTP 200`). This keeps the default `presigned` upload
   mode, where destination credentials never leave the manager.
2. **Else, only if the control test proves query auth broken,** opt the
   destination into direct upload:
   - Set the destination's `upload_mode` to `direct` (default is
     `presigned`; per-job credential issuance is the fallback path).
   - **Warnings — read before enabling:**
     - The manager delivers per-job destination credentials to the agent
       over the agent channel: credential material leaves the manager on
       every job instead of staying server-side behind short-lived grants.
     - Transport must be TLS end to end (agent↔manager and agent↔storage);
       never enable `direct` over plain HTTP outside an isolated lab.
     - Audit implications: uploads authenticate as the destination identity
       rather than via single-purpose presigned grants, so per-URL
       attribution in storage-side access logs is weaker.
     - **Never enable `direct` unless step 2 of the control test above
       returns `403 SignatureDoesNotMatch`.** It is a workaround for a
       server defect, not a performance or convenience option.

> Field-name note: `upload_mode: presigned|direct` (default `presigned`)
> documents the intended end state being built alongside this doc; exact
> placement/shape follows the credential-issuance track and this file will
> be reconciled to it at integration.
