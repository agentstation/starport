---
title: Configure
area: configure
order: 0
summary: Set configuration values, resolve provider secrets, find each file and directory, and manage settings from the console.
---

This area is for an operator who sets or changes the settings of a gateway. It explains where Starport reads each value and which source wins.

## What this area covers

- The sources of a value and their order of precedence.
- Secret references and secret manager wrappers for provider inference credentials.
- The default directories and files on each platform.
- The configuration section of the console and the checks that a save runs.

## Generated reference pages

When the build includes them, this area also holds two generated reference pages. The settings inventory lists each setting with its default. The file inventory lists each managed file and directory for each platform. The build makes these pages from the code of the release.

## Inspect the effective configuration

Run `starport config show` to see the effective values without secrets. Run `starport config validate` to check the same configuration that `starport serve` loads.
