package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/jung-kurt/gofpdf"
)

// Estrutura de entrada da aplicação
type RespostasInput struct {
	Funcionario string         `json:"funcionario"`
	Dimensoes   map[string][]int `json:"dimensoes"` // Ex: "Exigencias": [5, 4, 5]
}

// Estrutura que o OpenClaw devolve
type OpenClawResponse struct {
	ScoreGeral   int    `json:"score_geral"`
	MatrizPGR    []Risk `json:"matriz_pgr"`
	Conclusao    string `json:"conclusao"`
}

type Risk struct {
	Dimensao      string `json:"dimensao"`
	Probabilidade int    `json:"probabilidade"`
	Severidade    int    `json:"severidade"`
	NivelRisco    string `json:"nivel_risco"`
	Recomendacao  string `json:"recomendacao"`
}

func main() {
	app := fiber.New()

	app.Post("/api/v1/pgr-psicossocial", func(c *fiber.Ctx) error {
		var input RespostasInput
		if err := c.BodyParser(&input); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "JSON inválido"})
		}

		// 1. Cálculo Aritmético Simples (0 a 100)
		scoresBrutos := make(map[string]float64)
		for nome, valores := range input.Dimensoes {
			soma := 0
			for _, v := range valores {
				soma += v
			}
			// Normalizando para 0-100 (Assumindo escala de 1 a 5 nas perguntas)
			scoresBrutos[nome] = (float64(soma) / float64(len(valores) * 5)) * 100
		}

		// 2. Comunicar com OpenClaw (Gemini)
		analise, err := consultarOpenClaw(scoresBrutos)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Falha no OpenClaw: " + err.Error()})
		}

		// 3. Gerar PDF
		pdfPath := fmt.Sprintf("pgr_%s.pdf", input.Funcionario)
		err = gerarPDF(input.Funcionario, analise, pdfPath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Erro ao gerar PDF"})
		}

		return c.Download(pdfPath)
	})

	log.Fatal(app.Listen(":3000"))
}

func consultarOpenClaw(dados map[string]float64) (*OpenClawResponse, error) {
	url := "http://localhost:8000/api/v1/run-skill/copsoq_nr1_expert"
	payload, _ := json.Marshal(map[string]interface{}{"input": dados})

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result OpenClawResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func gerarPDF(nome string, data *OpenClawResponse, path string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	// Cabeçalho
	pdf.SetFont("Arial", "B", 16)
	pdf.Cell(0, 10, "Inventario de Riscos Psicossociais - NR-01")
	pdf.Ln(12)

	pdf.SetFont("Arial", "", 12)
	pdf.Cell(0, 10, fmt.Sprintf("Funcionario: %s", nome))
	pdf.Ln(10)
	pdf.Cell(0, 10, fmt.Sprintf("Score Global de Saude Mental: %d/100", data.ScoreGeral))
	pdf.Ln(15)

	// Tabela da Matriz
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(50, 8, "Dimensao", "1", 0, "C", false, 0, "")
	pdf.CellFormat(30, 8, "Prob (1-5)", "1", 0, "C", false, 0, "")
	pdf.CellFormat(30, 8, "Sever (1-5)", "1", 0, "C", false, 0, "")
	pdf.CellFormat(40, 8, "Nivel de Risco", "1", 1, "C", false, 0, "")

	pdf.SetFont("Arial", "", 10)
	for _, r := range data.MatrizPGR {
		pdf.CellFormat(50, 8, r.Dimensao, "1", 0, "L", false, 0, "")
		pdf.CellFormat(30, 8, fmt.Sprintf("%d", r.Probabilidade), "1", 0, "C", false, 0, "")
		pdf.CellFormat(30, 8, fmt.Sprintf("%d", r.Severidade), "1", 0, "C", false, 0, "")
		pdf.CellFormat(40, 8, r.NivelRisco, "1", 1, "C", false, 0, "")
	}

	pdf.Ln(10)
	pdf.SetFont("Arial", "B", 12)
	pdf.Cell(0, 10, "Conclusao e Plano de Acao:")
	pdf.Ln(8)
	pdf.SetFont("Arial", "", 10)
	pdf.MultiCell(0, 5, data.Conclusao, "", "", false)

	return pdf.OutputFileAndClose(path)
}