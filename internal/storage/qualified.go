package storage

// QualifiedValkeyVersion is the Valkey release that the real-backend qualification covers.
// CI pins the same release by image digest, and TestQualifiedValkeyVersion compares a real service against it.
// The qualified mode is one standalone writable primary. Another release or Cluster mode is UNVERIFIED
// until a qualification run records it here.
const QualifiedValkeyVersion = "7.2.14"
