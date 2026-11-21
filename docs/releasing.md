# Releasing

Every merge to `main` ships. There is no manual release step in the normal flow.

```
merge to main → Test workflow → Deploy workflow → tag vX.Y.Z → Release workflow
                                                          (goreleaser, Homebrew tap)
```

1. `Test` runs on the merge commit.
2. When `Test` finishes green, `Deploy` runs `scripts/next_version.sh`, pushes
   the tag, and dispatches `Release` for it. The dispatch is explicit because a
   tag pushed with the default `GITHUB_TOKEN` does not start other workflows.
3. `Release` builds, signs and publishes with goreleaser, and updates the
   Homebrew tap.

## Version rules

`next_version.sh` reads commit subjects since the last `vX.Y.Z` tag.

| Commit | Bump |
|---|---|
| `type!:` subject, or a `BREAKING CHANGE` footer | major |
| anything else (`feat:`, `fix:`, `chore:`, …) | patch |

It prints nothing, and Deploy does nothing, when HEAD is already tagged.

## Cut a minor release by hand

Minor bumps are never inferred. Tag the commit yourself:

```bash
git fetch origin
git tag -a v0.7.0 -m v0.7.0 origin/main
git push origin v0.7.0
```

The tag push starts `Release` through its `push: tags` trigger.

## Re-run a release that failed

A failed `Release` leaves the tag in place. Deploy does not retry it, because
HEAD is already tagged. Dispatch `Release` again for the same tag:

```bash
gh workflow run release.yml --ref main -f tag=vX.Y.Z
```

The input must be an exact `vX.Y.Z`. Check the result with:

```bash
gh run list --workflow release.yml --limit 3
```

After a release, confirm the commit reached `main`:

```bash
git fetch -q origin && git merge-base --is-ancestor <sha> origin/main \
  && echo "in main" || echo "NOT in main"
```

## Rotate a secret

`Release` uses three repository secrets:

| Secret | Used for |
|---|---|
| `MACOS_SIGN_P12` | The macOS signing certificate, base64 encoded |
| `MACOS_SIGN_P12_PASSWORD` | Its password |
| `HOMEBREW_TAP_TOKEN` | A token that can push to the `homebrew-moomux` tap repository |

Set the new value, then re-run a release to test it:

```bash
gh secret set HOMEBREW_TAP_TOKEN
gh secret set MACOS_SIGN_P12 < cert.p12.b64
gh secret set MACOS_SIGN_P12_PASSWORD
```

Keep the same signing certificate across releases when you can. The macOS
permission grants described in the README survive upgrades only while the
signature stays the same.
