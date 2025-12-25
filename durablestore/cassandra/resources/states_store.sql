CREATE TABLE IF NOT EXISTS states_store (
    version_number  bigint,
    persistence_id  text,
    state_payload   blob,
    state_manifest  text,
    timestamp       bigint,
    shard_number    bigint,
    PRIMARY KEY (shard_number, persistence_id)
);
