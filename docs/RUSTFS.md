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
- Interoperating tools doing the same thing: SDK PUT presigners
  (`aws-sdk-go-v2` `PutObject`, boto3 `put_object`, …) + `curl -X PUT` —
  anything that signs a PUT into the query string instead of headers.
  (Note: `aws s3 presign` mints GET-only URLs per the AWS CLI reference,
  so it cannot produce the PUT probe — step 2 below uses boto3.)

Header-auth SigV4 (`aws s3 cp`, SDK clients with static credentials) keeps
working, which is exactly what makes this defect confusing: credentials,
buckets, and networking are all fine.

## Affected versions

- Reported against **RustFS 1.0.0**. RustFS versions are not exhaustively
  mapped — **verify for your build** by running the control test below
  against your own endpoint; do not trust version numbers alone.

## Control test (proves it on any S3 endpoint)

Run from any host with AWS CLI v2, `curl`, and python3 with boto3
(`pip install boto3` if missing — step 2 needs it). Uses placeholders
only — substitute your endpoint, bucket, and credentials. Use the same
region your destination is configured with (`us-east-1` below is the
common default for S3-compatible servers).

```bash
export AWS_ACCESS_KEY_ID=<your-access-key-id>
export AWS_SECRET_ACCESS_KEY=<your-secret-access-key>
export AWS_EC2_METADATA_DISABLED=true
ENDPOINT=http://<s3-host>:9000
BUCKET=<bucket>
echo hello > /tmp/vaultix-probe.txt

# 1. Positive control: header-auth upload. Expect success.
aws --endpoint-url "$ENDPOINT" --region us-east-1 s3 cp \
  /tmp/vaultix-probe.txt s3://$BUCKET/vaultix-probe/header-auth.txt

# 2. Query-auth probe: presigned PUT. Capture the HTTP status + body.
# `aws s3 presign` MUST NOT be used here: it mints GET-only URLs, and
# PUTing to a GET-signed URL fails with SignatureDoesNotMatch on every
# server (including AWS) — a false positive. Mint a real PUT URL:
URL=$(python3 - "$ENDPOINT" "$BUCKET" <<'EOF'
import boto3, os, sys
from botocore.config import Config
endpoint, bucket = sys.argv[1], sys.argv[2]
s3 = boto3.client('s3', endpoint_url=endpoint,
    region_name='us-east-1',
    aws_access_key_id=os.environ['AWS_ACCESS_KEY_ID'],
    aws_secret_access_key=os.environ['AWS_SECRET_ACCESS_KEY'],
    config=Config(signature_version='s3v4', s3={'addressing_style': 'path'}))
print(s3.generate_presigned_url('put_object',
    Params={'Bucket': bucket, 'Key': 'vaultix-probe/query-auth.txt'},
    ExpiresIn=300))
EOF
)
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
addressing error, the CLI is attempting virtual-hosted style: run
`aws configure set s3.addressing_style path` first — there is no
`AWS_S3_ADDRESSING_STYLE` env var, the config file is the only switch.
The boto3 snippet in step 2 already pins path style.)

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

> Field-name note (verified at integration): the destination-level field
> is `upload_mode: presigned|direct` (default `presigned`, API + storage),
> and a polled direct-mode job carries `upload_mode: "direct"` plus a
> seven-field `destination_credential` (`access_key_id`,
> `secret_access_key`, `endpoint`, `region`, `bucket`, `use_path_style`,
> `prefix`).
