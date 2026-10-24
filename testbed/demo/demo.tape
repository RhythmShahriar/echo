# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# The 60-second demo: a plugin update breaks the admin email. A WordPress
# shop on staging (the testbed's), a snapshot before the update, the
# update (scenario s01: the forms plugin's 2.0 resets "Notify the site
# owner" to off) and the compare that catches it.
#
# Render it with the testbed up: make -C testbed demo (needs VHS,
# https://github.com/charmbracelet/vhs). It writes docs/assets/demo.gif.

Output ../../docs/assets/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1280
Set Height 720
Set Padding 24
Set TypingSpeed 35ms

Hide
Type "source ./setup.sh" Enter
Sleep 4s
Type "clear" Enter
Show

Type "# A WordPress shop on staging, sealed by Echo. Before the plugin update:" Enter
Sleep 1s
Type `rhythm echo snapshot --action "Contact form" --name before` Enter
Sleep 9s

Type "# Update the forms plugin to 2.0 (the testbed's s01)" Enter
Type "./toggle.sh s01 broken" Enter
Sleep 2s

Type `rhythm echo compare --action "Contact form" --snapshot before` Enter
Sleep 12s

Type "# The update stopped the admin email. Nothing left staging." Enter
Sleep 6s

Hide
Type "source ./teardown.sh" Enter
Sleep 2s
