const fs = require('fs');

const filePath = 'internal/workers/infrastructure/build-response/handler.go';
const content = fs.readFileSync(filePath, 'utf8');

const marker = '// ===== LISTING PAGE BUILDER =====';
const idx = content.indexOf(marker);
if (idx === -1) {
    console.error('Marker not found');
    process.exit(1);
}

const before = content.substring(0, idx);
const after = content.substring(idx);

const helpers = `func getStringVal(m map[string]interface{}, key string, fallback string) string {
	if m == nil {
		return fallback
	}
	if val, ok := m[key]; ok && val != nil {
		if s, ok := val.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", val)
	}
	return fallback
}

func getMapVal(m map[string]interface{}, key string) map[string]interface{} {
	if m == nil {
		return nil
	}
	if val, ok := m[key]; ok && val != nil {
		if mm, ok := val.(map[string]interface{}); ok {
			return mm
		}
	}
	return nil
}

func getArrayVal(m map[string]interface{}, key string) []interface{} {
	if m == nil {
		return nil
	}
	if val, ok := m[key]; ok && val != nil {
		if arr, ok := val.([]interface{}); ok {
			return arr
		}
	}
	return nil
}

`;

fs.writeFileSync(filePath, before + helpers + after);
console.log('Restored helpers successfully!');
