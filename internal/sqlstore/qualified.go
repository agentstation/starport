package sqlstore

// QualifiedPostgreSQLVersion is the PostgreSQL release that the real-backend qualification covers.
// CI pins the same release by image digest, and TestQualifiedPostgreSQLVersion compares a real service against it.
// Another release is UNVERIFIED until a qualification run records it here.
const QualifiedPostgreSQLVersion = "16.15"
