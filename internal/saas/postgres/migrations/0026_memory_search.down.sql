DROP INDEX IF EXISTS saas_memories_search_document_gin;
DROP TRIGGER IF EXISTS saas_memories_search_document ON saas_memories;
DROP FUNCTION IF EXISTS saas_refresh_memory_search_document();
ALTER TABLE saas_memories DROP COLUMN IF EXISTS search_document;
