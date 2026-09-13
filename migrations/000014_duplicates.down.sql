DROP TRIGGER IF EXISTS todos_duplicate_cleanup ON todos;
DROP TRIGGER IF EXISTS events_duplicate_cleanup ON events;
DROP TABLE IF EXISTS duplicate_candidates;
DROP FUNCTION IF EXISTS duplicate_candidates_cleanup();