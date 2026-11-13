package bridge

import (
	"github.com/db-fly/db-fly/internal/app"
)

// ImportFile 把 SQL 脚本或结构化数据文件导入指定连接。
// DryRun=true 仅生成预览，绝不写库；DryRun=false 才真正执行。
func (b *Bridge) ImportFile(req ImportRequest) (ImportOutcomeDTO, error) {
	out, err := b.app.ImportFile(b.ctx(), req.Conn, req.Ref, req.Path, app.ImportOptions{
		Format:      req.Format,
		Sheet:       req.Sheet,
		HasHeader:   req.HasHeader,
		BatchSize:   req.BatchSize,
		Transaction: req.Transaction,
		DryRun:      req.DryRun,
	})
	if err != nil {
		return ImportOutcomeDTO{}, err
	}
	return ImportOutcomeDTO{
		Format:     out.Format,
		Rows:       out.Rows,
		Statements: out.Statements,
		Preview:    out.Preview,
		Applied:    out.Result.Applied,
	}, nil
}
