// Package tagguide holds the shared instructions for agent tag skills and commands.
package tagguide

const Description = "Automatically tag the current moomux session as soon as you confirm a ticket or open PR belongs to its work, including immediately after creating the PR. Use even when only one link is known."

const Instructions = `Apply this workflow as soon as you confirm a ticket or open pull request belongs to the current moomux session. Do it in the same turn, including immediately after successful PR creation using the returned URL. Don't wait until both links are known or for the user to invoke the skill.

Read the stored tags with ` + "`moomux tag`" + ` (no flags) from the current session. If that fails, stop without writing.

- Use a ticket URL only when the ticket is for this session's work. A link mentioned for background, comparison, or another task isn't a tag. If the user has decided there is no ticket, don't infer, look up, add, or clear a ticket; an open PR can still be tagged on its own.
- Use a PR URL only when the PR is open and belongs to this session's work. When you need to check the current branch, use ` + "`gh pr view --json url,state,title,body`" + ` and check that the state is ` + "`OPEN`" + `. Reuse a URL and state already confirmed in the conversation or by a tool.
- On explicit invocation, use any links already confirmed and look for missing ones. The current branch, its open PR title or body, and recent commit messages can help identify the work's ticket when none is tracked. Resolve a ticket ID to its actual tracker URL (such as Asana, Jira, or Linear); don't guess from an ambiguous ID or contextual link. If neither link can be confirmed, report that on explicit invocation and leave the tags alone.

Compare the confirmed URLs with the stored values and pass only flags for tags that need to change:

    moomux tag -ticket '<ticket URL>'
    moomux tag -pr '<PR URL>'
    moomux tag -ticket '<ticket URL>' -pr '<PR URL>'

An omitted flag preserves the other tag, including when adding it later. If every confirmed URL already matches, skip the update. Report an update as successful only if the command succeeds.
`
