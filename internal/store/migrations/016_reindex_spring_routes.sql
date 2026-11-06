-- Java and Kotlin files now yield routes (Spring @GetMapping, @RequestMapping,
-- ...). Blank the hashes of Java and Kotlin files, as 015 did for Go, so the
-- next normal index re-indexes them and extracts their routes: the ones whose
-- symbols are Java or Kotlin, plus any file with a Java or Kotlin extension.
-- Other languages keep their hashes. No row is deleted here.
UPDATE files SET content_hash = ''
WHERE EXISTS (
        SELECT 1 FROM symbols s
        WHERE s.repo_name = files.repo_name
          AND s.file_path = files.path
          AND s.language IN ('java', 'kotlin')
    )
    OR path LIKE '%.java'
    OR path LIKE '%.kt'
    OR path LIKE '%.kts';
