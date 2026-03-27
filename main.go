package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gorilla/websocket"
	"github.com/jung-kurt/gofpdf"
)

// Estruturas para comunicação com OpenClaw
type RespostasInput struct {
	Funcionario string              `json:"funcionario"`
	Dimensoes   map[string][]int    `json:"dimensoes"`
}

type Risk struct {
	Dimensao      string `json:"dimensao"`
	Probabilidade int    `json:"probabilidade"`
	Severidade    int    `json:"severidade"`
	NivelRisco    string `json:"nivel_risco"`
	CorPGR        string `json:"cor_pgr"` // Verde, Amarelo, Laranja, Vermelho
	Recomendacao  string `json:"recomendacao_nr1"`
}

type OpenClawResponse struct {
	ScoreGeral   int    `json:"score_geral_saude"`
	MatrizPGR    []Risk `json:"matriz_pgr"`
	Conclusao    string `json:"conclusao_diagnostica"`
}

func main() {
	app := fiber.New()

	app.Post("/api/v1/pgr-psicossocial", func(c *fiber.Ctx) error {
		var input RespostasInput
		if err := c.BodyParser(&input); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
		}

		// 1. Cálculo Aritmético com Inversão de Polaridade (Baseado na Tese)
		scoresBrutos := calcularScoresCopsoqBr(input.Dimensoes)

		// 2. Chamada ao OpenClaw/Gemini
		analise, err := consultarOpenClaw(input.Funcionario, scoresBrutos)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Erro no OpenClaw: " + err.Error()})
		}

		// 3. Geração do PDF Colorido
		pdfPath := fmt.Sprintf("relatorio_nr1_%s.pdf", strings.ReplaceAll(input.Funcionario, " ", "_"))
		err = gerarPDFColorido(input.Funcionario, analise, pdfPath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Erro ao gerar PDF"})
		}

		return c.Download(pdfPath)
	})

	log.Fatal(app.Listen("0.0.0.0:3000"))
}

// Lógica baseada na adaptação cultural do COPSOQ II-Br
func calcularScoresCopsoqBr(dimensoes map[string][]int) map[string]float64 {
	resultados := make(map[string]float64)

	// Dimensões onde score ALTO é RUIM (Exigências, Insegurança, Conflitos)
	// Dimensões onde score BAIXO é RUIM (Apoio Social, Influência, Sentido, Recompensas)
	recursosTrabalho := map[string]bool{
		"Influência": true, "Possibilidades de Desenvolvimento": true,
		"Sentido do Trabalho": true, "Compromisso com o local de Trabalho": true,
		"Previsibilidade": true, "Suporte Social": true, "Qualidade da Liderança": true,
		"Recompensas": true, "Confiança Horizontal": true, "Justiça": true,
	}

	for nome, valores := range dimensoes {
		soma := 0
		for _, v := range valores {
			soma += v
		}
		media := (float64(soma) / float64(len(valores) * 5)) * 100

		if recursosTrabalho[nome] {
			// Se for um recurso, invertemos para o Gemini entender que 0 é o pior risco
			// Ou enviamos o valor real e deixamos a Skill inverter.
			// Recomendado: Enviar o valor real (0-100) e a Skill interpreta.
			resultados[nome] = media
		} else {
			resultados[nome] = media
		}
	}
	return resultados
}

func consultarOpenClaw(funcionario string, dados map[string]float64) (*OpenClawResponse, error) {
    url := "ws://127.0.0.1:18789/api/v1/run-skill/copsoq_nr1_expert"
    token := "psicologia_nr1_secret_2026"

    var conn *websocket.Conn
    var err error
    dialer := websocket.Dialer{}
    header := http.Header{}
    header.Add("Authorization", "Bearer "+token)

    // Tenta conectar até 3 vezes com intervalo de 2 segundos
    for i := 0; i < 3; i++ {
        conn, _, err = dialer.Dial(url, header)
        if err == nil {
            break
        }
        fmt.Printf("Tentativa %d: OpenClaw ainda não respondeu, aguardando...\n", i+1)
        time.Sleep(2 * time.Second)
    }

    if err != nil {
        return nil, fmt.Errorf("não foi possível conectar ao OpenClaw após várias tentativas: %v", err)
    }
    defer conn.Close()

	payload := map[string]interface{}{
		"input": map[string]interface{}{
			"funcionario": funcionario,
			"scores":      dados,
		},
	}

	if err := conn.WriteJSON(payload); err != nil {
		return nil, err
	}

	var result OpenClawResponse
	if err := conn.ReadJSON(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func gerarPDFColorido(nome string, data *OpenClawResponse, path string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	// Estilos de Cores (RGB) para a Matriz NR-01
	cores := map[string][]int{
		"Verde":    {144, 238, 144},
		"Amarelo":  {255, 255, 0},
		"Laranja":  {255, 165, 0},
		"Vermelho": {255, 99, 71},
	}

	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(0, 10, "Inventário de Riscos Psicossociais (NR-01 / COPSOQ II-Br)")
	pdf.Ln(12)

	pdf.SetFont("Arial", "", 11)
	pdf.Cell(0, 10, fmt.Sprintf("Funcionário: %s", nome))
	pdf.Ln(8)
	pdf.Cell(0, 10, fmt.Sprintf("Índice de Saúde Mental Global: %d/100", data.ScoreGeral))
	pdf.Ln(15)

	// Cabeçalho da Tabela
	pdf.SetFillColor(200, 200, 200)
	pdf.SetFont("Arial", "B", 9)
	pdf.CellFormat(60, 10, "Dimensão Psicossocial", "1", 0, "C", true, 0, "")
	pdf.CellFormat(20, 10, "Prob (P)", "1", 0, "C", true, 0, "")
	pdf.CellFormat(20, 10, "Sever (S)", "1", 0, "C", true, 0, "")
	pdf.CellFormat(40, 10, "Nível de Risco", "1", 0, "C", true, 0, "")
	pdf.CellFormat(50, 10, "Ação Recomendada", "1", 1, "C", true, 0, "")

	// Linhas da Matriz
	pdf.SetFont("Arial", "", 8)
	for _, r := range data.MatrizPGR {
		c, ok := cores[r.CorPGR]
		if !ok { c = []int{255, 255, 255} }

		pdf.CellFormat(60, 10, r.Dimensao, "1", 0, "L", false, 0, "")
		pdf.CellFormat(20, 10, fmt.Sprintf("%d", r.Probabilidade), "1", 0, "C", false, 0, "")
		pdf.CellFormat(20, 10, fmt.Sprintf("%d", r.Severidade), "1", 0, "C", false, 0, "")

		// Célula Colorida para o Nível de Risco
		pdf.SetFillColor(c[0], c[1], c[2])
		pdf.CellFormat(40, 10, r.NivelRisco, "1", 0, "C", true, 0, "")

		pdf.CellFormat(50, 10, r.Recomendacao, "1", 1, "L", false, 0, "")
	}

	pdf.Ln(10)
	pdf.SetFont("Arial", "B", 11)
	pdf.Cell(0, 10, "Conclusão Diagnóstica e Parecer Técnico:")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)
	pdf.MultiCell(0, 5, data.Conclusao, "", "L", false)

	return pdf.OutputFileAndClose(path)
}