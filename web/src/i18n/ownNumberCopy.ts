type Copy = { title: string; hint: string; save: string; clear: string; changed: string }
const copies: Record<string, Copy> = {
  'zh-CN': { title: '本机号码', hint: '设备未读到号码时可手动补填，仅用于这张 SIM 的显示，不写入 SIM。留空并保存可清除。', save: '保存号码', clear: '清除', changed: 'SIM 或线路已变化，请刷新后重新设置。' },
  'zh-TW': { title: '本機號碼', hint: '裝置未讀到號碼時可手動補填，只供這張 SIM 顯示，不寫入 SIM。留空儲存可清除。', save: '儲存號碼', clear: '清除', changed: 'SIM 或線路已變更，請重新整理後設定。' },
  'en-US': { title: 'Own number', hint: 'Supply a display number when the device cannot read it. Bound to this SIM; never written to the SIM. Save an empty value to clear.', save: 'Save number', clear: 'Clear', changed: 'The SIM or line changed. Refresh before saving.' },
  'de-DE': { title: 'Eigene Nummer', hint: 'Nummer für diese SIM manuell ergänzen. Keine Änderung der SIM. Leer speichern zum Löschen.', save: 'Nummer speichern', clear: 'Löschen', changed: 'SIM oder Leitung geändert. Bitte aktualisieren.' },
  'fr-FR': { title: 'Numéro de la SIM', hint: 'Compléter le numéro affiché pour cette SIM, sans écrire sur la SIM. Enregistrer vide pour effacer.', save: 'Enregistrer', clear: 'Effacer', changed: 'SIM ou ligne modifiée. Actualisez avant de continuer.' },
  'es-ES': { title: 'Número propio', hint: 'Completa el número mostrado para esta SIM sin escribir en ella. Guarda vacío para borrar.', save: 'Guardar número', clear: 'Borrar', changed: 'La SIM o línea cambió. Actualiza antes de guardar.' },
  'pt-BR': { title: 'Número próprio', hint: 'Informe o número exibido desta SIM sem alterar a SIM. Salve vazio para limpar.', save: 'Salvar número', clear: 'Limpar', changed: 'O SIM ou a linha mudou. Atualize antes de salvar.' },
  'ja-JP': { title: '自分の電話番号', hint: '読取できない番号をこの SIM の表示用に設定します。SIM には書き込みません。空欄を保存すると解除します。', save: '番号を保存', clear: '解除', changed: 'SIM または回線が変わりました。更新してください。' },
  'vi-VN': { title: 'Số của SIM', hint: 'Bổ sung số hiển thị cho SIM này, không ghi vào SIM. Lưu ô trống để xóa.', save: 'Lưu số', clear: 'Xóa', changed: 'SIM hoặc đường dây đã đổi. Hãy làm mới trước khi lưu.' }
}
export function ownNumberCopy(locale: string): Copy { return copies[locale] || copies['en-US']! }
