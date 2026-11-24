# Apt & dpkg History Viewer


I want to create a standalone history view generator for apt & dpkg installation history.
This should compile to a single static binary with no runtime dependecies.
It should look at the apt & dpkg logs on the current system and write out an interactive HTML & SVG
visualization of the apt & dpkg history on that system.
It should show all package installations & upgrades, with markers for manual installations and other manual actions,
as well as automatic ones.
