# AGENTS.md

Code is the source of truth for application behavior.

Do not treat documentation as normative, unless it's `docs/adr/` or `GLOSSARY.md`.

Read `docs/prep` when refining the project plan or milestone specs; those documents describe intended behavior and are normative.

Ignore `.devnotes` and `docs/personal`

## Implementation 

Don't comment code, except where it does something extermely unusual.

When done with implementing a single ticket, do not commit until approved.

Frontend should assume a width of >=1280px to be available.

Skip token-expensive frontend tests that involve actual browser usage or claude code preview. 
When reviewing your implementation, I will eyeball the results, just give me a checklist.


## Agent skills

For issue-tracker configuration, read `docs/agents/issue-tracker.md`.
Use the standard triage labels defined by the skills; there are no repo-specific overrides.
Use one root `GLOSSARY.md` and `docs/adr/` for domain documentation.
Invoke the relevant skills for their workflows; these instructions only configure repository choices.
