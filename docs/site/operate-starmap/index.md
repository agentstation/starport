---
title: Operate Starmap
area: operate-starmap
order: 0
summary: Run the Starmap catalog server and the catalog inputs that a Starport deployment reads.
---

This area is for operators who run Starmap next to a Starport deployment. Starmap owns the catalog facts. Starport reads them through one connected runtime in each gateway process.

A small deployment needs no separate Starmap server. Each gateway reads the public channel and keeps its own catalog state. A large fleet or a restricted network can use a central Starmap server. A deployment with no route to the internet reads a catalog file that an operator moves across the boundary.

This area tells you how to run a central server and how to supply a catalog file. It also tells you how to keep Starmap state safe during a recovery. Starmap state includes journals and locks. Do not delete them by hand.

Starport shows no removal targets. Starmap owns the rename and restore behavior of catalog records.
