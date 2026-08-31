# Pull requests

## Reviewing

* Submitting a pull request using AI tooling does not obviate a reviewer of the responsibility to validate the generated comments. It is **not** the responsibility of the change's author to spend time determining which feedback is actually valid.

## Template

Apply these rules when filing. They are repeated inside the template's HTML comments below, but those comments do not reliably survive into an AI assistant's context, so they are stated here in plain text as well:

* Prefix the PR title with the icon matching the change type: ⚠️ breaking or major, ✨ feature, 🐛 bugfix or patch, 📖 documentation or proposal, 🌱 minor or other.
* Prefix the subject with `WIP:` while the PR is unfinished.
* Fill in the `release-note` block, or write `NONE` in it when no release note is needed. Include the string `action required` when users upgrading must take action.
* Delete every HTML comment from the PR body before submitting.

The following content after the `BEGIN TEMPLATE` line and before the `END TEMPLATE` line is the template for submitting a pull-request description in GitHub:

<!-- BEGIN TEMPLATE -->
<!--
Thanks for sending a pull request!

Please add one of the following icons to the title of this PR:

    ⚠️ (:warning:, a major or breaking change)
    ✨ (:sparkles:, feature additions)
    🐛 (:bug:, patch and bugfixes)
    📖 (:book:, documentation or proposals)
    🌱 (:seedling:, minor or other)

Some other tips:

    1. If this is your first time filing a PR, please read our contributor
       guidelines for submitting a change at https://vm-operator.readthedocs.io/en/stable/start/contrib/submit-change/.
    2. If this PR is unfinished, please prefix the subject with "WIP:".

Finally, before filing the PR, please delete all of the HTML comments.
-->

**What does this PR do, and why is it needed?**

<!-- A clear and concise description of the PR and the problem it solves / feature it introduces / value it adds to the project. -->


**Which issue(s) is/are addressed by this PR?** *(optional, in `fixes #<issue number>(, fixes #<issue_number>, ...)` format, will close the issue(s) when PR gets merged)*:

Fixes #


**Are there any special notes for your reviewer**:

<!-- Anything else you would like the reviewers to know about this PR. -->


**Please add a release note if necessary**:

<!--
Write your release note:

    1. Enter your extended release note in the below block. If the PR requires
       additional action from users switching to the new release, please include
       the string "action required".
    2. If a release note is not required, please write "NONE".
-->

```release-note

```
<!-- END TEMPLATE -->

