---
title: Storage
area: storage
order: 0
summary: Select the storage backends, back up and restore a stopped deployment, move between storage modes, and set the optional caches.
---

This area is for an operator who selects, protects, or changes the storage of a gateway. Starport keeps its durable state in three roles: a KV store, a SQL store, and a blob store for file bytes.

## What this area covers

- The three storage roles, the backend for each role, and the local files that each recipe needs.
- Backup sets of a stopped deployment and the checks before a restore.
- The limits of a move from one storage mode to another.
- The optional response, discovery, extraction, and semantic caches.

## Two recipes

The local recipe uses Badger, SQLite, and the file system. The shared recipe uses Valkey, PostgreSQL, and object storage. With Valkey, Starport refuses other SQL and file backends. Refer to [Select storage](../architecture/storage-selection.md) to select a recipe for a target.

## Back up before a change

A change of a storage setting does not copy data. Make and verify a backup before each storage change.
