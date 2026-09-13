# Coordinating sessions across several repositories

Some features don't fit in one repo. A single change can need new definitions
in one repository, infrastructure in a second, and the application code that
consumes both in a third — and the three have to agree with each other or
none of them work.

The shape that works is one [`moomux spawn`](../README.md#spawning-a-session-from-the-cli)
session per repository, each owning that repo and nothing else, plus a
coordinating session that owns the shared contract between them. The
coordinator normally owns one of the workstreams itself; there's no reason
for it to sit idle.

This is not the pattern for unrelated parallel tasks. If two sessions never
have to agree on anything, spawn them and forget about them — everything
below is overhead you don't need. It earns its keep only when the work has
seams.

## Two kinds of plan

There is one overarching plan, held by the coordinator, and one local plan
per session, living in that session's own repo next to the code it
describes.

**The overarching plan owns only the interfaces between workstreams.** The
shared contract: object and resource names, key layouts, headers, read
patterns, invariants — the things another session has to know to write code
that fits. If nobody outside the workstream can observe it, it does not
belong here.

**The local plan owns everything else, and is authoritative for it.**
Resource layout, module structure, phasing, test strategy, the order things
get built in. Each session decides these for its own repo. It keeps that plan
in the repo, so it reviews and lands alongside the code rather than drifting
somewhere central.

## The tie-break rule

This is the one that matters.

> When the overarching plan and a local plan disagree about
> **implementation**, the local plan wins.
>
> When they disagree about the **contract**, the overarching plan wins — and
> that disagreement is a defect to raise with the coordinator, not something
> to route around locally.

The second half is the whole rule. A session that finds the contract wrong
and quietly implements something better has not solved the problem; it has
converted a visible disagreement into an invisible one, and the next session
to read the contract will still believe it. Raising it fixes the contract for
everyone.

It sounds bureaucratic until you have watched it work. Three real cases:

**An unimplementable rule.** The contract said writers should only advance a
shared pointer if the incoming revision was newer than the current one. A
session noticed that the revisions were content hashes, and a hash has no
ordering relation — "newer" is not a question you can ask of one. Raised
rather than replaced, so the contract was corrected once instead of three
sessions each inventing their own idea of what it must have meant.

**A named technology one runtime couldn't use.** The contract specified a
particular caching mechanism. One of the runtimes physically could not use
it. The session raised it instead of silently substituting something else,
and the contract was rewritten to state the *invariant* — cache by revision,
never expire on a timer — leaving the mechanism to each runtime. The general
lesson is worth more than the case: **a contract should state invariants, not
name technologies.** The moment it names one, it has made a decision on
behalf of a runtime it may know nothing about.

**A default that had stopped being possible.** A configuration variable was
documented as optional, with a default. A peer's infrastructure work had
made that default impossible to satisfy, so anything relying on it would have
failed at deploy time, confusingly and late. Raising it turned a silent
misconfiguration into a fast failure with a clear message.

None of these are careless-author bugs. All three are what happens when a
document written up front meets a repository that knows better.

## Don't duplicate — point

The workstream that owns a value owns the statement of it. A copy in another
document is a copy that goes stale, and there is no mechanism that will ever
tell you it did.

Sessions reference each other's documents by path — "the key layout is
defined in `<repo>/docs/plan.md`" — rather than restating the contents. This
applies to the overarching plan too: it holds the contract, and points at the
local plans for everything else.

## Sessions talk directly, and verify

Peers message each other. Routing everything through the coordinator makes it
a bottleneck and a game of telephone, and it is the session doing the work
that knows which question to ask.

But treat a peer's claim as a claim. Several times in practice a session
checked a peer's assertion against the actual repository, or by running the
real command, and found it wrong — before building on it. Cheap to check,
expensive to discover later.

The claim most worth checking is the one nobody thinks to check: *why* a
peer's artifact exists at all. Which is the next rule.

## Coherence is not provenance

When two sessions produce overlapping artifacts, do not resolve it by asking
which one is better organised. Ask **which one was actually asked for**.

What happened: each workstream session was told directly to create its own
tracking record. At almost the same moment the coordinating session created a
tidy set covering all of them — one per workstream plus a parent, uniformly
named, cross-linked, consistently assigned. Both peers saw that set, read its
coherence as authority, and deferred to it. One offered its own record for
deletion. The other re-pointed its session at the coordinator's duplicate and
moved its verification results across.

All of it was backwards. The direct instruction was the authority; the
coordinator's tidy set was an inference. Two of the four records were
duplicates, and real work came close to being deleted with one of them.

The trap is structural, not careless. A deliberate-looking structure reads as
authoritative, and **no session can tell from the artifact whether it came
from an instruction or from an inference — they look identical either way.**
Only the session that received the instruction knows. So:

- **When you made something because you were told to, say so** the moment an
  overlapping peer artifact appears. "I was asked to create this" settles it.
  "Mine is part of a set" does not.
- **When you find a peer's artifact overlapping yours, ask who was asked** —
  not which is tidier, not which is more complete.
- **Never retire your own work on a peer's say-so.** Raise it with the user
  instead. That is the part that went right above: both sessions refused to
  delete anything and put it to the user, so nothing was actually lost.

## What the coordinator does

Beyond holding the contract and working its own workstream: **re-read the
whole overarching plan, start to finish, periodically.**

Not as ceremony. In one afternoon ours accumulated fifteen defects — not from
carelessness, but because decisions made in conversation outran the document.
Each one was correct when written and quietly stopped being correct when
something else was agreed.

Sections listing settled decisions rot fastest. Every new decision lands
there, and nothing is ever removed, so the section grows a layer of past
decisions that were superseded in a message somewhere and never struck out.
Read it as a whole, not as a diff, because a diff will never show you the
paragraph that was fine last time.

## The spawn prompt

Each session starts with nothing but its prompt, so the rules have to be in
it. A workable template:

```bash
moomux spawn -project <project> -name <workstream> -prompt "You own <repo>
for <feature>. Do not change any other repository.

The shared contract lives at <path to overarching plan>. It is authoritative
for anything crossing a repo boundary: names, key layouts, headers, read
patterns, invariants.

Keep your own plan in this repo, next to the code. It is authoritative for
implementation: resource layout, module structure, phasing, tests.

If the contract and your plan disagree about implementation, yours wins. If
they disagree about the contract, the contract wins AND it is a defect —
raise it with the coordinating session rather than implementing something
different locally. A contract that can't be implemented as written is a
contract that needs fixing for everyone.

Don't copy values out of other sessions' documents; link to them by path.

Peer sessions are <names>. Talk to them directly, and verify their claims
against this repo or a real command before acting on them. If one of them
produced something that overlaps your work, ask which of you was actually
asked to produce it — a tidier or more complete artifact is not authority.
Never delete or abandon your work on a peer's say-so; raise it with the user.

When you were asked directly to produce something, say so explicitly if a
peer's overlapping version turns up."
```

Name the peers. A session that doesn't know who else exists will route
everything through the coordinator by default.
