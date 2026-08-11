ALTER TABLE saas_memories ADD COLUMN search_document tsvector;

CREATE FUNCTION saas_refresh_memory_search_document() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    keyword_text text;
BEGIN
    SELECT COALESCE(string_agg(concat_ws(' ', item->>'term', item->>'display'), ' '), '')
    INTO keyword_text
    FROM jsonb_array_elements(COALESCE(NEW.keywords, '[]'::jsonb)) AS item;

    NEW.search_document := to_tsvector(
        'simple',
        concat_ws(
            ' ',
            NEW.content,
            NEW.memory_type,
            NEW.source_kind,
            array_to_string(NEW.entities, ' '),
            array_to_string(NEW.tags, ' '),
            keyword_text
        )
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER saas_memories_search_document
BEFORE INSERT OR UPDATE OF content, memory_type, source_kind, entities, tags, keywords
ON saas_memories
FOR EACH ROW EXECUTE FUNCTION saas_refresh_memory_search_document();

UPDATE saas_memories SET content=content;
ALTER TABLE saas_memories ALTER COLUMN search_document SET NOT NULL;

CREATE INDEX saas_memories_search_document_gin
ON saas_memories USING gin(search_document);
