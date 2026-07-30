# Legacy User Refresh Design

Legacy users with small vocabularies should be invited to opt into the current
onboarding experience. Their accounts must not be reset by an administrator or
by inactivity alone. The bot will send a one-time invitation to an explicit
allowlist, and each user can decide whether to start over through `/start`.

The existing reset flow asks for `RESET MY DATA`, deletes the user's learning
data immediately, and only then starts language selection. That ordering leaves
an account empty when a user abandons onboarding or when a later operation
fails. The refreshed flow will instead record that a reset is pending. Existing
vocabulary, settings, and sessions remain usable until the user chooses both
languages and taps **Initialize**.

Final provisioning is the commit point. In one database transaction, the
service verifies that starter vocabulary exists for the selected language pair,
removes the old vocabulary, settings, and active training/game sessions,
creates the replacement starter vocabulary and default settings, and clears the
onboarding state. Historical game statistics remain intact. Any failure rolls
back the entire transaction, preserving the old account.

New-user onboarding uses the same provisioning service without replacement.
The reset-pending marker is stored in `OnboardingState`, so restarts do not lose
the user's intent. Starting `/start` again returns an existing user to the reset
warning, and canceling that warning clears only onboarding state.

Outreach is intentionally a small operator tool rather than permanent campaign
infrastructure. It accepts only an explicit comma-separated user-ID allowlist,
validates and deduplicates it, performs a dry run by default, and requires
`-send` for delivery. It sends no database updates. Telegram delivery failures,
including users who blocked the bot, are reported per recipient and leave all
account data unchanged.
