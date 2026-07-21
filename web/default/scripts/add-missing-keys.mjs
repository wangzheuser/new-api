import fs from 'node:fs/promises'
import path from 'node:path'

const LOCALES_DIR = path.resolve('src/i18n/locales')

function stableStringify(obj) {
  return `${JSON.stringify(obj, null, 2)}\n`
}

const newKeys = {
  en: {
    '{{count}} registration code(s) deleted':
      '{{count}} registration code(s) deleted',
    '{{count}} registration code(s) updated':
      '{{count}} registration code(s) updated',
    'Copy selected registration codes': 'Copy selected registration codes',
    'Delete selected registration codes': 'Delete selected registration codes',
    'Delete selected registration codes?':
      'Delete selected registration codes?',
    'Disable selected registration codes':
      'Disable selected registration codes',
    'Enable selected registration codes': 'Enable selected registration codes',
    'Filter by name or registration code...':
      'Filter by name or registration code...',
    'Generate a registration code or adjust the filters.':
      'Generate a registration code or adjust the filters.',
    'No registration codes found': 'No registration codes found',
    'Not started': 'Not started',
    'Partially used': 'Partially used',
    'Registration codes copied!': 'Registration codes copied!',
    'Store complete request and response bodies for this channel. Global conversation capture must also be enabled.':
      'Store complete request and response bodies for this channel. Global conversation capture must also be enabled.',
    'This will delete {{count}} selected registration code(s). Usage records will be retained.':
      'This will delete {{count}} selected registration code(s). Usage records will be retained.',
    'registration code': 'registration code',
  },
  zh: {
    '{{count}} registration code(s) deleted': '已删除 {{count}} 个注册码',
    '{{count}} registration code(s) updated': '已更新 {{count}} 个注册码',
    'Copy selected registration codes': '复制选中的注册码',
    'Delete selected registration codes': '删除选中的注册码',
    'Delete selected registration codes?': '删除选中的注册码？',
    'Disable selected registration codes': '禁用选中的注册码',
    'Enable selected registration codes': '启用选中的注册码',
    'Filter by name or registration code...': '按名称或注册码筛选...',
    'Generate a registration code or adjust the filters.':
      '请生成注册码或调整筛选条件。',
    'No registration codes found': '未找到注册码',
    'Not started': '未生效',
    'Partially used': '部分使用',
    'Registration codes copied!': '注册码已复制！',
    'Store complete request and response bodies for this channel. Global conversation capture must also be enabled.':
      '存储此渠道的完整请求与响应内容，同时必须开启全局对话采集。',
    'This will delete {{count}} selected registration code(s). Usage records will be retained.':
      '将删除选中的 {{count}} 个注册码，使用记录会保留。',
    'registration code': '注册码',
  },
  'zh-TW': {
    '{{count}} registration code(s) deleted': '已刪除 {{count}} 個註冊碼',
    '{{count}} registration code(s) updated': '已更新 {{count}} 個註冊碼',
    'Copy selected registration codes': '複製選取的註冊碼',
    'Delete selected registration codes': '刪除選取的註冊碼',
    'Delete selected registration codes?': '刪除選取的註冊碼？',
    'Disable selected registration codes': '停用選取的註冊碼',
    'Enable selected registration codes': '啟用選取的註冊碼',
    'Filter by name or registration code...': '依名稱或註冊碼篩選...',
    'Generate a registration code or adjust the filters.':
      '請產生註冊碼或調整篩選條件。',
    'No registration codes found': '找不到註冊碼',
    'Not started': '尚未生效',
    'Partially used': '部分使用',
    'Registration codes copied!': '註冊碼已複製！',
    'Store complete request and response bodies for this channel. Global conversation capture must also be enabled.':
      '儲存此渠道的完整請求與回應內容，同時必須啟用全域對話採集。',
    'This will delete {{count}} selected registration code(s). Usage records will be retained.':
      '將刪除選取的 {{count}} 個註冊碼，使用記錄會保留。',
    'registration code': '註冊碼',
  },
  fr: {
    '{{count}} registration code(s) deleted':
      '{{count}} code(s) d’inscription supprimé(s)',
    '{{count}} registration code(s) updated':
      '{{count}} code(s) d’inscription mis à jour',
    'Copy selected registration codes':
      'Copier les codes d’inscription sélectionnés',
    'Delete selected registration codes':
      'Supprimer les codes d’inscription sélectionnés',
    'Delete selected registration codes?':
      'Supprimer les codes d’inscription sélectionnés ?',
    'Disable selected registration codes':
      'Désactiver les codes d’inscription sélectionnés',
    'Enable selected registration codes':
      'Activer les codes d’inscription sélectionnés',
    'Filter by name or registration code...':
      'Filtrer par nom ou code d’inscription...',
    'Generate a registration code or adjust the filters.':
      'Générez un code d’inscription ou modifiez les filtres.',
    'No registration codes found': 'Aucun code d’inscription trouvé',
    'Not started': 'Pas encore actif',
    'Partially used': 'Partiellement utilisé',
    'Registration codes copied!': 'Codes d’inscription copiés !',
    'Store complete request and response bodies for this channel. Global conversation capture must also be enabled.':
      'Stocke les requêtes et réponses complètes de ce canal. La capture globale des conversations doit aussi être activée.',
    'This will delete {{count}} selected registration code(s). Usage records will be retained.':
      'Cette action supprimera {{count}} code(s) d’inscription sélectionné(s). Les journaux d’utilisation seront conservés.',
    'registration code': 'code d’inscription',
  },
  ja: {
    '{{count}} registration code(s) deleted':
      '{{count}} 個の登録コードを削除しました',
    '{{count}} registration code(s) updated':
      '{{count}} 個の登録コードを更新しました',
    'Copy selected registration codes': '選択した登録コードをコピー',
    'Delete selected registration codes': '選択した登録コードを削除',
    'Delete selected registration codes?': '選択した登録コードを削除しますか？',
    'Disable selected registration codes': '選択した登録コードを無効化',
    'Enable selected registration codes': '選択した登録コードを有効化',
    'Filter by name or registration code...':
      '名前または登録コードで絞り込み...',
    'Generate a registration code or adjust the filters.':
      '登録コードを生成するか、フィルターを調整してください。',
    'No registration codes found': '登録コードが見つかりません',
    'Not started': '有効期間前',
    'Partially used': '一部使用済み',
    'Registration codes copied!': '登録コードをコピーしました！',
    'Store complete request and response bodies for this channel. Global conversation capture must also be enabled.':
      'このチャネルのリクエストとレスポンス全文を保存します。グローバル会話キャプチャも有効にする必要があります。',
    'This will delete {{count}} selected registration code(s). Usage records will be retained.':
      '選択した {{count}} 個の登録コードを削除します。使用記録は保持されます。',
    'registration code': '登録コード',
  },
  ru: {
    '{{count}} registration code(s) deleted':
      'Удалено кодов регистрации: {{count}}',
    '{{count}} registration code(s) updated':
      'Обновлено кодов регистрации: {{count}}',
    'Copy selected registration codes': 'Копировать выбранные коды регистрации',
    'Delete selected registration codes': 'Удалить выбранные коды регистрации',
    'Delete selected registration codes?':
      'Удалить выбранные коды регистрации?',
    'Disable selected registration codes':
      'Отключить выбранные коды регистрации',
    'Enable selected registration codes': 'Включить выбранные коды регистрации',
    'Filter by name or registration code...':
      'Фильтр по имени или коду регистрации...',
    'Generate a registration code or adjust the filters.':
      'Создайте код регистрации или измените фильтры.',
    'No registration codes found': 'Коды регистрации не найдены',
    'Not started': 'Ещё не действует',
    'Partially used': 'Частично использован',
    'Registration codes copied!': 'Коды регистрации скопированы!',
    'Store complete request and response bodies for this channel. Global conversation capture must also be enabled.':
      'Сохраняет полные запросы и ответы этого канала. Глобальный сбор диалогов также должен быть включён.',
    'This will delete {{count}} selected registration code(s). Usage records will be retained.':
      'Будет удалено выбранных кодов регистрации: {{count}}. Записи использования сохранятся.',
    'registration code': 'код регистрации',
  },
  vi: {
    '{{count}} registration code(s) deleted': 'Đã xóa {{count}} mã đăng ký',
    '{{count}} registration code(s) updated':
      'Đã cập nhật {{count}} mã đăng ký',
    'Copy selected registration codes': 'Sao chép mã đăng ký đã chọn',
    'Delete selected registration codes': 'Xóa mã đăng ký đã chọn',
    'Delete selected registration codes?': 'Xóa các mã đăng ký đã chọn?',
    'Disable selected registration codes': 'Tắt mã đăng ký đã chọn',
    'Enable selected registration codes': 'Bật mã đăng ký đã chọn',
    'Filter by name or registration code...': 'Lọc theo tên hoặc mã đăng ký...',
    'Generate a registration code or adjust the filters.':
      'Hãy tạo mã đăng ký hoặc điều chỉnh bộ lọc.',
    'No registration codes found': 'Không tìm thấy mã đăng ký',
    'Not started': 'Chưa có hiệu lực',
    'Partially used': 'Đã dùng một phần',
    'Registration codes copied!': 'Đã sao chép mã đăng ký!',
    'Store complete request and response bodies for this channel. Global conversation capture must also be enabled.':
      'Lưu toàn bộ nội dung yêu cầu và phản hồi của kênh này. Thu thập hội thoại toàn cục cũng phải được bật.',
    'This will delete {{count}} selected registration code(s). Usage records will be retained.':
      'Thao tác này sẽ xóa {{count}} mã đăng ký đã chọn. Bản ghi sử dụng vẫn được giữ lại.',
    'registration code': 'mã đăng ký',
  },
}

async function main() {
  let totalAdded = 0

  for (const [locale, trans] of Object.entries(newKeys)) {
    const filePath = path.join(LOCALES_DIR, `${locale}.json`)
    const json = JSON.parse(await fs.readFile(filePath, 'utf8'))

    let count = 0
    for (const [key, value] of Object.entries(trans)) {
      if (!Object.hasOwn(json.translation, key)) {
        json.translation[key] = value
        count++
      } else if (json.translation[key] !== value) {
        json.translation[key] = value
        count++
      }
    }

    if (count > 0) {
      json.translation = Object.fromEntries(
        Object.entries(json.translation).sort(([a], [b]) => a.localeCompare(b))
      )
      await fs.writeFile(filePath, stableStringify(json), 'utf8')
    }

    console.log(`${locale}: ${count} translations applied`)
    totalAdded += count
  }

  console.log(`\nTotal: ${totalAdded} translations applied`)
}

main().catch((err) => {
  console.error(err)
  process.exitCode = 1
})
